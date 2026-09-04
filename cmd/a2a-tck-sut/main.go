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
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	v1 "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1/pbconv"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/push"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	// The A2A gRPC binding maps an unsupported service version to
	// UNIMPLEMENTED. Return an explicit status so the SDK's generic
	// FailedPrecondition mapping cannot hide the wire-level contract.
	return ctx, nil, status.Error(codes.Unimplemented, "this version is not supported: supported version is "+protocolVersion)
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

// grpcTCKHandler keeps the SDK's conversion layer while making task
// subscriptions explicit for the fixture.
type grpcTCKHandler struct {
	*a2agrpc.Handler
	requestHandler a2asrv.RequestHandler
	state          *sutTaskState
}

func (h *grpcTCKHandler) SubscribeToTask(req *v1.SubscribeToTaskRequest, stream grpc.ServerStreamingServer[v1.StreamResponse]) error {
	converted, err := pbconv.FromProtoSubscribeToTaskRequest(req)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	// The SDK's default event queue closes as soon as the deterministic
	// executor returns INPUT_REQUIRED. Keep the fixture's observable task alive
	// and poll its state so multiple gRPC subscribers receive the same ordered
	// snapshots and can observe a later continuation.
	if h.state != nil {
		task, ok := h.state.snapshot(converted.ID)
		if !ok {
			return status.Error(codes.NotFound, "task not found")
		}
		if task.Status.State.Terminal() {
			return status.Error(codes.FailedPrecondition, "subscription is not supported for terminal tasks")
		}
		if err := sendGRPCTask(stream, task); err != nil {
			return err
		}
		last := task.Status.State
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stream.Context().Done():
				return nil
			case <-ticker.C:
				updated, exists := h.state.snapshot(converted.ID)
				if !exists {
					return status.Error(codes.NotFound, "task not found")
				}
				if updated.Status.State != last {
					if err := sendGRPCTask(stream, updated); err != nil {
						return err
					}
					last = updated.Status.State
					if updated.Status.State.Terminal() {
						return nil
					}
				}
			}
		}
	}
	for event, eventErr := range h.requestHandler.SubscribeToTask(stream.Context(), converted) {
		if eventErr != nil {
			if errors.Is(eventErr, a2a.ErrTaskNotFound) {
				return status.Error(codes.NotFound, "task not found")
			}
			if errors.Is(eventErr, a2a.ErrUnsupportedOperation) {
				return status.Error(codes.FailedPrecondition, "subscription is not supported")
			}
			return status.Error(codes.Internal, eventErr.Error())
		}
		response, convertErr := pbconv.ToProtoStreamResponse(event)
		if convertErr != nil {
			return status.Error(codes.Internal, convertErr.Error())
		}
		if sendErr := stream.Send(response); sendErr != nil {
			return status.Error(codes.Aborted, sendErr.Error())
		}
	}
	return nil
}

func sendGRPCTask(stream grpc.ServerStreamingServer[v1.StreamResponse], task *a2a.Task) error {
	response, err := pbconv.ToProtoStreamResponse(task)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if err := stream.Send(response); err != nil {
		return status.Error(codes.Aborted, err.Error())
	}
	return nil
}

// nonNilTaskStore keeps the REST representation conformant for an empty task
// list. The upstream in-memory store returns a nil slice, which JSON encodes
// as null while the A2A schema requires an array.
type nonNilTaskStore struct{ taskstore.Store }

func (s nonNilTaskStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	response, err := s.Store.List(ctx, req)
	if err != nil {
		return nil, err
	}
	if response.Tasks == nil {
		response.Tasks = []*a2a.Task{}
	}
	return response, nil
}

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
		// A continuation against either resumable interruption completes the
		// existing task while preserving the provider-owned Task/Context IDs.
		if execCtx.StoredTask != nil && (execCtx.StoredTask.Status.State == a2a.TaskStateInputRequired || execCtx.StoredTask.Status.State == a2a.TaskStateAuthRequired) {
			_ = emit(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart("continuation complete")))
			_ = emit(statusEvent(execCtx, a2a.TaskStateCompleted))
			return
		}
		switch {
		case strings.HasPrefix(id, "tck-input-required"):
			_ = emit(statusEvent(execCtx, a2a.TaskStateInputRequired))
		case strings.HasPrefix(id, "tck-auth-required"):
			_ = emit(statusEvent(execCtx, a2a.TaskStateAuthRequired))
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
	grpcListen := flag.String("grpc-listen", "127.0.0.1:10000", "gRPC listen address when the grpc binding is selected")
	grpcURL := flag.String("grpc-url", "", "gRPC target advertised in the Agent Card; defaults to --grpc-listen")
	binding := flag.String("binding", "jsonrpc", "A2A binding advertised by the SUT: jsonrpc, http_json, or grpc")
	flag.Parse()
	if *publicURL == "" {
		*publicURL = "http://" + *listen
	}
	var protocolBinding a2a.TransportProtocol
	switch strings.ToLower(strings.TrimSpace(*binding)) {
	case "jsonrpc":
		protocolBinding = a2a.TransportProtocolJSONRPC
	case "http_json", "http+json":
		protocolBinding = a2a.TransportProtocolHTTPJSON
	case "grpc":
		protocolBinding = a2a.TransportProtocolGRPC
	default:
		log.Fatalf("unsupported A2A binding %q", *binding)
	}
	if *grpcURL == "" {
		*grpcURL = *grpcListen
	}
	interfaceURL := *publicURL
	if protocolBinding == a2a.TransportProtocolGRPC {
		interfaceURL = *grpcURL
	}
	card := &a2a.AgentCard{
		Name: "Agent Federation Hub Repository TCK SUT", Version: "1.0.0",
		Description:         "Repository-owned deterministic A2A v1 compatibility fixture",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(interfaceURL, protocolBinding)},
		Capabilities:        a2a.AgentCapabilities{Streaming: true, PushNotifications: true},
		DefaultInputModes:   []string{"text"}, DefaultOutputModes: []string{"text", "application/json", "text/plain"},
		Skills: []a2a.AgentSkill{{ID: "tck", Name: "A2A TCK fixture", Description: "Deterministic protocol scenarios", Tags: []string{"tck"}}},
	}
	state := newSUTTaskState()
	pushStore := push.NewInMemoryStore()
	pushSender := push.NewHTTPPushSender(&push.HTTPSenderConfig{AllowPrivateNetworks: true, Timeout: 5 * time.Second})
	tasks := nonNilTaskStore{Store: taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})}
	handler := a2asrv.NewHandler(tckExecutor{state: state},
		a2asrv.WithTaskStore(tasks), a2asrv.WithPushNotifications(pushStore, pushSender),
		a2asrv.WithCapabilityChecks(&a2a.AgentCapabilities{Streaming: true, PushNotifications: true}),
		a2asrv.WithCallInterceptors(versionInterceptor{}))
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	var protocolHandler http.Handler
	switch protocolBinding {
	case a2a.TransportProtocolJSONRPC:
		protocolHandler = guardedJSONRPCHandler(state, a2asrv.NewJSONRPCHandler(handler))
	case a2a.TransportProtocolHTTPJSON:
		protocolHandler = guardedRESTHandler(state, a2asrv.NewRESTHandler(handler))
	case a2a.TransportProtocolGRPC:
		// The HTTP listener serves the discovery Card while the actual A2A
		// methods are exposed by the separate gRPC listener below.
		protocolHandler = http.NotFoundHandler()
	}
	mux.Handle("/", protocolHandler)
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute}
	listener, err := net.Listen("tcp4", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("A2A TCK SUT listening on %s", *listen)
	errs := make(chan error, 2)
	go func() { errs <- server.Serve(listener) }()
	if protocolBinding == a2a.TransportProtocolGRPC {
		grpcListener, listenErr := net.Listen("tcp4", *grpcListen)
		if listenErr != nil {
			log.Fatal(listenErr)
		}
		grpcServer := grpc.NewServer()
		grpcTCKHandler := &grpcTCKHandler{Handler: a2agrpc.NewHandler(handler), requestHandler: handler, state: state}
		v1.RegisterA2AServiceServer(grpcServer, grpcTCKHandler)
		log.Printf("A2A TCK SUT gRPC listening on %s", *grpcListen)
		go func() { errs <- grpcServer.Serve(grpcListener) }()
	}
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, grpc.ErrServerStopped) {
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
		body = normalizePushTaskIDAlias(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// normalizePushTaskIDAlias accepts the snake_case task_id emitted by the
// pinned TCK's JSON-RPC client while preserving the canonical taskId wire
// field used by the A2A v1 JSON-RPC binding. This compatibility shim is kept
// inside the repository-owned fixture and does not change the Hub adapter.
func normalizePushTaskIDAlias(body []byte) []byte {
	var request struct {
		Method string                     `json:"method"`
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Params == nil {
		return body
	}
	switch request.Method {
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
	default:
		return body
	}
	value, ok := request.Params["task_id"]
	if !ok {
		return body
	}
	if _, exists := request.Params["taskId"]; !exists {
		request.Params["taskId"] = value
	}
	delete(request.Params, "task_id")
	// Preserve the original request envelope (including its request ID) when
	// rebuilding the body; only the params field is intentionally changed.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	params, err := json.Marshal(request.Params)
	if err != nil {
		return body
	}
	envelope["params"] = params
	result, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return result
}

// guardedRESTHandler supplies the subscription semantics required by the
// protocol when the deterministic executor has already returned. The SDK's
// default local event queue treats that case as "no active execution", while
// a provider may keep the observable Task available for later subscriptions.
func guardedRESTHandler(state *sutTaskState, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := strings.TrimSpace(r.Header.Get(a2a.SvcParamVersion))
		if version != "" && version != protocolVersion {
			writeRESTErrorResponse(w, http.StatusBadRequest, "FAILED_PRECONDITION", "this version is not supported")
			return
		}
		if strings.HasSuffix(r.URL.Path, ":subscribe") {
			taskID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tasks/"), ":subscribe")
			if taskID != "" && !strings.Contains(taskID, "/") {
				handleRESTSubscribeOverride(w, r, a2a.TaskID(taskID), state)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func handleRESTSubscribeOverride(w http.ResponseWriter, r *http.Request, taskID a2a.TaskID, state *sutTaskState) {
	task, ok := state.snapshot(taskID)
	if !ok || task.Status.State.Terminal() {
		writeRESTErrorResponse(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	writeSSEHeaders(w)
	if !writeRESTSSEEvent(w, &a2a.StreamResponse{Event: task}, state) {
		return
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
			return
		case <-ticker.C:
			updated, exists := state.snapshot(taskID)
			if !exists {
				return
			}
			if updated.Status.State.Terminal() {
				status := &a2a.TaskStatusUpdateEvent{TaskID: updated.ID, ContextID: updated.ContextID, Status: updated.Status}
				_ = writeRESTSSEEvent(w, &a2a.StreamResponse{Event: status}, state)
				if flush != nil {
					flush.Flush()
				}
				return
			}
		}
	}
}

func writeRESTSSEEvent(w http.ResponseWriter, response *a2a.StreamResponse, state *sutTaskState) bool {
	payload, err := json.Marshal(response)
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

func writeRESTErrorResponse(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": code, "status": status, "message": message,
	}})
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
