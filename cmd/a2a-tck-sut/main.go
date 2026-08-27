// Command a2a-tck-sut exposes a repository-owned A2A v1 JSON-RPC/SSE SUT.
//
// It intentionally implements deterministic message-ID scenarios used by the
// pinned A2A TCK. This process is a conformance fixture, not a production
// provider runtime or a shortcut around the Hub's opaque-agent boundary.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const protocolVersion = "1.0"

type versionInterceptor struct{}

func (versionInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	callCtx.User = a2asrv.NewAuthenticatedUser("repository-tck-client", nil)
	params := callCtx.ServiceParams()
	if params == nil {
		return ctx, nil, nil
	}
	values, ok := params.Get(a2a.SvcParamVersion)
	if !ok || len(values) == 0 || strings.TrimSpace(values[0]) == "" || values[0] == protocolVersion {
		return ctx, nil, nil
	}
	return ctx, nil, fmt.Errorf("%w: supported version is %s", a2a.ErrVersionNotSupported, protocolVersion)
}

func (versionInterceptor) After(ctx context.Context, callCtx *a2asrv.CallContext, response *a2asrv.Response) error {
	return nil
}

type sutTaskState struct {
	mu    sync.RWMutex
	tasks map[a2a.TaskID]*a2a.Task
	seq   atomic.Uint64
}

func newSUTTaskState() *sutTaskState {
	return &sutTaskState{tasks: make(map[a2a.TaskID]*a2a.Task)}
}

func (s *sutTaskState) observe(event a2a.Event) {
	if s == nil || event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch value := event.(type) {
	case *a2a.Task:
		s.tasks[value.ID] = cloneTask(value)
	case *a2a.TaskStatusUpdateEvent:
		task := s.ensure(value.TaskID, value.ContextID)
		task.Status = value.Status
	case *a2a.TaskArtifactUpdateEvent:
		task := s.ensure(value.TaskID, value.ContextID)
		if value.Artifact == nil {
			return
		}
		if value.Append && len(task.Artifacts) > 0 {
			last := task.Artifacts[len(task.Artifacts)-1]
			if last.ID == value.Artifact.ID {
				last.Parts = append(last.Parts, value.Artifact.Parts...)
				return
			}
		}
		task.Artifacts = append(task.Artifacts, value.Artifact)
	}
}

func (s *sutTaskState) recordMessage(message *a2a.Message) {
	if s == nil || message == nil || message.TaskID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.ensure(message.TaskID, message.ContextID)
	task.History = append(task.History, message)
}

func (s *sutTaskState) ensure(id a2a.TaskID, contextID string) *a2a.Task {
	task := s.tasks[id]
	if task == nil {
		task = &a2a.Task{ID: id, ContextID: contextID}
		s.tasks[id] = task
	}
	if task.ContextID == "" {
		task.ContextID = contextID
	}
	return task
}

func (s *sutTaskState) snapshot(id a2a.TaskID) (*a2a.Task, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func cloneTask(task *a2a.Task) *a2a.Task {
	if task == nil {
		return nil
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		copy := *task
		return &copy
	}
	var clone a2a.Task
	if err := json.Unmarshal(encoded, &clone); err != nil {
		copy := *task
		return &copy
	}
	return &clone
}

type tckExecutor struct{ state *sutTaskState }

func (t tckExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		emit := func(event a2a.Event) bool {
			if t.state != nil {
				t.state.observe(event)
			}
			return yield(event, nil)
		}
		message := execCtx.Message
		if message == nil {
			_ = yield(statusEvent(execCtx, a2a.TaskStateFailed), nil)
			return
		}
		id := message.ID
		if t.state != nil && execCtx.StoredTask != nil {
			t.state.recordMessage(message)
		}
		// Direct Message results are the only execution path that must not create
		// a Task before returning the response.
		if strings.HasPrefix(id, "tck-message-response") {
			_ = emit(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Direct message response")))
			return
		}
		if execCtx.StoredTask == nil {
			if !emit(a2a.NewSubmittedTask(execCtx, message)) {
				return
			}
		}
		// A continuation against INPUT_REQUIRED completes the existing task.
		if execCtx.StoredTask != nil && execCtx.StoredTask.Status.State == a2a.TaskStateInputRequired {
			_ = emit(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart("continuation complete")))
			_ = emit(statusEvent(execCtx, a2a.TaskStateCompleted))
			return
		}
		switch {
		case strings.HasPrefix(id, "tck-input-required"):
			_ = emit(statusEvent(execCtx, a2a.TaskStateInputRequired))
		case strings.HasPrefix(id, "tck-reject-task"):
			_ = emit(statusEvent(execCtx, a2a.TaskStateRejected))
		case strings.HasPrefix(id, "tck-artifact-text"):
			completeWithArtifact(emit, execCtx, a2a.NewTextPart("Generated text content"))
		case strings.HasPrefix(id, "tck-artifact-file-url"):
			part := a2a.NewFileURLPart(a2a.URL("https://example.com/output.txt"), "text/plain")
			part.Filename = "output.txt"
			completeWithArtifact(emit, execCtx, part)
		case strings.HasPrefix(id, "tck-artifact-file"):
			part := a2a.NewRawPart([]byte("tck"))
			part.MediaType, part.Filename = "text/plain", "output.txt"
			completeWithArtifact(emit, execCtx, part)
		case strings.HasPrefix(id, "tck-artifact-data"):
			completeWithArtifact(emit, execCtx, a2a.NewDataPart(map[string]any{"key": "value", "count": float64(42)}))
		case strings.HasPrefix(id, "tck-stream-artifact-chunked"):
			first := a2a.NewArtifactEvent(execCtx, a2a.NewTextPart("chunk-1 "))
			if !emit(first) {
				return
			}
			second := a2a.NewArtifactUpdateEvent(execCtx, first.Artifact.ID, a2a.NewTextPart("chunk-2"))
			second.LastChunk = true
			if !emit(second) {
				return
			}
			_ = emit(statusEvent(execCtx, a2a.TaskStateCompleted))
		case strings.HasPrefix(id, "tck-stream-artifact-text"):
			completeWithArtifact(emit, execCtx, a2a.NewTextPart("Streamed text content"))
		case strings.HasPrefix(id, "tck-stream-artifact-file"):
			part := a2a.NewRawPart([]byte("tck"))
			part.MediaType, part.Filename = "text/plain", "output.txt"
			completeWithArtifact(emit, execCtx, part)
		case strings.HasPrefix(id, "tck-stream-ordering-001"):
			completeWithArtifact(emit, execCtx, a2a.NewTextPart("Ordered output"))
		case strings.HasPrefix(id, "test-resubscribe-message-id"):
			if !emit(statusEvent(execCtx, a2a.TaskStateWorking)) {
				return
			}
			timer := time.NewTimer(4 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			_ = emit(statusEvent(execCtx, a2a.TaskStateCompleted))
		default:
			completeWithArtifact(emit, execCtx, a2a.NewTextPart("Hello from repository-owned A2A TCK SUT"))
		}
	}
}

func (t tckExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		event := statusEvent(execCtx, a2a.TaskStateCanceled)
		if t.state != nil {
			t.state.observe(event)
		}
		_ = yield(event, nil)
	}
}

func completeWithArtifact(emit func(a2a.Event) bool, execCtx *a2asrv.ExecutorContext, part *a2a.Part) {
	if !emit(a2a.NewArtifactEvent(execCtx, part)) {
		return
	}
	_ = emit(statusEvent(execCtx, a2a.TaskStateCompleted))
}

func statusEvent(execCtx *a2asrv.ExecutorContext, state a2a.TaskState) *a2a.TaskStatusUpdateEvent {
	event := a2a.NewStatusUpdateEvent(execCtx, state, nil)
	if event.Status.Timestamp != nil {
		utc := event.Status.Timestamp.UTC()
		event.Status.Timestamp = &utc
	}
	return event
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9999", "HTTP listen address")
	publicURL := flag.String("public-url", "", "public base URL used in the Agent Card")
	flag.Parse()
	if *publicURL == "" {
		*publicURL = "http://" + *listen
	}
	card := &a2a.AgentCard{
		Name: "Agent Federation Hub Repository TCK SUT", Version: "1.0.0",
		Description:         "Repository-owned deterministic A2A v1 JSON-RPC/SSE compatibility fixture",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(*publicURL, a2a.TransportProtocolJSONRPC)},
		Capabilities:        a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:   []string{"text"}, DefaultOutputModes: []string{"text", "application/json", "text/plain"},
		Skills: []a2a.AgentSkill{{ID: "tck", Name: "A2A TCK fixture", Description: "Deterministic protocol scenarios", Tags: []string{"tck"}}},
	}
	state := newSUTTaskState()
	handler := a2asrv.NewHandler(tckExecutor{state: state}, a2asrv.WithCallInterceptors(versionInterceptor{}))
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", guardedJSONRPCHandler(state, a2asrv.NewJSONRPCHandler(handler)))
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute}
	listener, err := net.Listen("tcp4", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("A2A TCK SUT listening on %s", *listen)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// guardedJSONRPCHandler makes unsupported service versions visible even when
// the SDK transport has not yet added a version interceptor of its own.
func guardedJSONRPCHandler(state *sutTaskState, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := strings.TrimSpace(r.Header.Get(a2a.SvcParamVersion))
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "request body could not be read", http.StatusBadRequest)
			return
		}
		if version != "" && version != protocolVersion {
			var request struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &request)
			writeJSONRPCError(w, request.ID, -32009, "this version is not supported")
			return
		}
		contentType := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])
		if contentType != "application/json" {
			var request struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &request)
			writeJSONRPCError(w, request.ID, -32005, "content type is not supported")
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) == nil {
			switch request.Method {
			case "GetTask":
				if handleGetTaskOverride(w, request.ID, request.Params, state) {
					return
				}
			case "SubscribeToTask":
				if handleSubscribeOverride(w, r, request.ID, request.Params, state) {
					return
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func handleGetTaskOverride(w http.ResponseWriter, id json.RawMessage, params json.RawMessage, state *sutTaskState) bool {
	var request struct {
		ID                 string `json:"id"`
		HistoryLength      *int   `json:"historyLength"`
		HistoryLengthSnake *int   `json:"history_length"`
	}
	if json.Unmarshal(params, &request) != nil || request.ID == "" {
		return false
	}
	task, ok := state.snapshot(a2a.TaskID(request.ID))
	if !ok {
		writeJSONRPCError(w, id, -32001, "task not found")
		return true
	}
	historyLength := request.HistoryLength
	if historyLength == nil {
		historyLength = request.HistoryLengthSnake
	}
	if historyLength != nil {
		length := *historyLength
		switch {
		case length <= 0:
			task.History = []*a2a.Message{}
		case len(task.History) > length:
			task.History = task.History[len(task.History)-length:]
		}
	}
	writeJSONRPCResult(w, id, map[string]any{"task": task})
	return true
}

func handleSubscribeOverride(w http.ResponseWriter, r *http.Request, id json.RawMessage, params json.RawMessage, state *sutTaskState) bool {
	var request struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(params, &request) != nil || request.ID == "" {
		return false
	}
	task, ok := state.snapshot(a2a.TaskID(request.ID))
	if !ok || task.Status.State.Terminal() {
		writeJSONRPCError(w, id, -32001, "task not found or terminal")
		return true
	}
	writeSSEHeaders(w)
	if !writeSSEEvent(w, id, &a2a.StreamResponse{Event: task}, state) {
		return true
	}
	flush, _ := w.(http.Flusher)
	if flush != nil {
		flush.Flush()
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return true
		case <-ticker.C:
			updated, exists := state.snapshot(a2a.TaskID(request.ID))
			if !exists {
				return true
			}
			if updated.Status.State.Terminal() {
				status := &a2a.TaskStatusUpdateEvent{TaskID: updated.ID, ContextID: updated.ContextID, Status: updated.Status}
				_ = writeSSEEvent(w, id, &a2a.StreamResponse{Event: status}, state)
				if flush != nil {
					flush.Flush()
				}
				return true
			}
		}
	}
}

func writeJSONRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	if len(id) == 0 {
		id = []byte("null")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	if len(id) == 0 {
		id = []byte("null")
	}
	reason := map[int]string{
		-32001: "TASK_NOT_FOUND",
		-32005: "CONTENT_TYPE_NOT_SUPPORTED",
		-32009: "VERSION_NOT_SUPPORTED",
	}[code]
	errorObject := map[string]any{"code": code, "message": message}
	if reason != "" {
		errorObject["data"] = []map[string]any{{
			"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
			"domain": "a2a-protocol.org",
			"reason": reason,
		}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": errorObject})
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSEEvent(w http.ResponseWriter, id json.RawMessage, response *a2a.StreamResponse, state *sutTaskState) bool {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(defaultJSONID(id)), "result": response})
	if err != nil {
		return false
	}
	sequence := uint64(1)
	if state != nil {
		sequence = state.seq.Add(1)
	}
	_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", strconv.FormatUint(sequence, 10), payload)
	return err == nil
}

func defaultJSONID(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}
