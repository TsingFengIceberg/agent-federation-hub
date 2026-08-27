// Command a2a-tck-sut exposes a repository-owned A2A v1 JSON-RPC/SSE SUT.
//
// It intentionally implements deterministic message-ID scenarios used by the
// pinned A2A TCK. This process is a conformance fixture, not a production
// provider runtime or a shortcut around the Hub's opaque-agent boundary.
package main

import (
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
	"strings"
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

type tckExecutor struct{}

func (tckExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		message := execCtx.Message
		if message == nil {
			_ = yield(statusEvent(execCtx, a2a.TaskStateFailed), nil)
			return
		}
		id := message.ID
		// Direct Message results are the only execution path that must not create
		// a Task before returning the response.
		if strings.HasPrefix(id, "tck-message-response") {
			_ = yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Direct message response")), nil)
			return
		}
		if execCtx.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(execCtx, message), nil) {
				return
			}
		}
		// A continuation against INPUT_REQUIRED completes the existing task.
		if execCtx.StoredTask != nil && execCtx.StoredTask.Status.State == a2a.TaskStateInputRequired {
			_ = yield(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart("continuation complete")), nil)
			_ = yield(statusEvent(execCtx, a2a.TaskStateCompleted), nil)
			return
		}
		switch {
		case strings.HasPrefix(id, "tck-input-required"):
			_ = yield(statusEvent(execCtx, a2a.TaskStateInputRequired), nil)
		case strings.HasPrefix(id, "tck-reject-task"):
			_ = yield(statusEvent(execCtx, a2a.TaskStateRejected), nil)
		case strings.HasPrefix(id, "tck-artifact-text"):
			completeWithArtifact(yield, execCtx, a2a.NewTextPart("Generated text content"))
		case strings.HasPrefix(id, "tck-artifact-file"):
			part := a2a.NewRawPart([]byte("tck"))
			part.MediaType, part.Filename = "text/plain", "output.txt"
			completeWithArtifact(yield, execCtx, part)
		case strings.HasPrefix(id, "tck-artifact-file-url"):
			part := a2a.NewFileURLPart(a2a.URL("https://example.com/output.txt"), "text/plain")
			part.Filename = "output.txt"
			completeWithArtifact(yield, execCtx, part)
		case strings.HasPrefix(id, "tck-artifact-data"):
			completeWithArtifact(yield, execCtx, a2a.NewDataPart(map[string]any{"key": "value", "count": float64(42)}))
		case strings.HasPrefix(id, "tck-stream-artifact-chunked"):
			first := a2a.NewArtifactEvent(execCtx, a2a.NewTextPart("chunk-1 "))
			if !yield(first, nil) {
				return
			}
			second := a2a.NewArtifactUpdateEvent(execCtx, first.Artifact.ID, a2a.NewTextPart("chunk-2"))
			second.LastChunk = true
			if !yield(second, nil) {
				return
			}
			_ = yield(statusEvent(execCtx, a2a.TaskStateCompleted), nil)
		case strings.HasPrefix(id, "tck-stream-artifact-text"):
			completeWithArtifact(yield, execCtx, a2a.NewTextPart("Streamed text content"))
		case strings.HasPrefix(id, "tck-stream-artifact-file"):
			part := a2a.NewRawPart([]byte("tck"))
			part.MediaType, part.Filename = "text/plain", "output.txt"
			completeWithArtifact(yield, execCtx, part)
		case strings.HasPrefix(id, "tck-stream-ordering-001"):
			completeWithArtifact(yield, execCtx, a2a.NewTextPart("Ordered output"))
		case strings.HasPrefix(id, "test-resubscribe-message-id"):
			if !yield(statusEvent(execCtx, a2a.TaskStateWorking), nil) {
				return
			}
			timer := time.NewTimer(4 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			_ = yield(statusEvent(execCtx, a2a.TaskStateCompleted), nil)
		default:
			completeWithArtifact(yield, execCtx, a2a.NewTextPart("Hello from repository-owned A2A TCK SUT"))
		}
	}
}

func (tckExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		_ = yield(statusEvent(execCtx, a2a.TaskStateCanceled), nil)
	}
}

func completeWithArtifact(yield func(a2a.Event, error) bool, execCtx *a2asrv.ExecutorContext, part *a2a.Part) {
	if !yield(a2a.NewArtifactEvent(execCtx, part), nil) {
		return
	}
	_ = yield(statusEvent(execCtx, a2a.TaskStateCompleted), nil)
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
	handler := a2asrv.NewHandler(tckExecutor{}, a2asrv.WithCallInterceptors(versionInterceptor{}))
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", guardedJSONRPCHandler(a2asrv.NewJSONRPCHandler(handler)))
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
func guardedJSONRPCHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := strings.TrimSpace(r.Header.Get(a2a.SvcParamVersion))
		if version != "" && version != protocolVersion {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			var request struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &request)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32009,"message":"this version is not supported"}}
`, defaultJSONID(request.ID))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func defaultJSONID(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}
