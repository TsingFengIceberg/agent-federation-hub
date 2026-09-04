// Command interop-hub is a provisional Hub-side A2A probe. It deliberately
// knows nothing about the remote Agent implementation behind its Agent Card.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

type outputEnvelope struct {
	Kind  string    `json:"kind"`
	Event a2a.Event `json:"event,omitempty"`
	Card  any       `json:"card,omitempty"`
}

func main() {
	cardURL := flag.String("agent-card-url", "", "base URL or complete Agent Card URL")
	operation := flag.String("operation", "send", "discover, send, stream, get, cancel, or subscribe")
	text := flag.String("text", "task", "text sent for send or stream")
	taskID := flag.String("task-id", "", "task ID used by task operations or message continuation")
	contextID := flag.String("context-id", "", "context ID used by message continuation")
	returnImmediately := flag.Bool("return-immediately", false, "return after the remote task is created")
	timeout := flag.Duration("timeout", 20*time.Second, "overall operation timeout")
	allowPrivate := flag.Bool("allow-private", false, "allow HTTP and private/local Agent URLs for development")
	flag.Parse()

	if *cardURL == "" {
		log.Fatal("--agent-card-url is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	policy := netpolicy.HTTPSOnlyPolicy()
	if *allowPrivate {
		policy.AllowPrivate = true
		policy.AllowHTTP = true
		policy.AllowedPorts = nil
	}
	httpClient := netpolicy.NewHTTPClient(*timeout, nil, policy)

	resolver := &agentcard.Resolver{Client: httpClient}
	card, err := resolver.Resolve(ctx, *cardURL)
	if err != nil {
		log.Fatalf("resolve Agent Card: %v", err)
	}
	if err := validateProfile(card); err != nil {
		log.Fatalf("Agent Card is outside ADR 0001 profile: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if *operation == "discover" {
		writeOutput(encoder, outputEnvelope{Kind: "agent-card", Card: card})
		return
	}

	client, err := a2aclient.NewFromCard(
		ctx,
		card,
		a2aclient.WithJSONRPCTransport(httpClient),
		a2aclient.WithConfig(a2aclient.Config{
			PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC},
		}),
	)
	if err != nil {
		log.Fatalf("create A2A client: %v", err)
	}
	defer func() {
		if err := client.Destroy(); err != nil {
			log.Printf("close A2A client: %v", err)
		}
	}()

	if err := run(ctx, encoder, client, *operation, *text, *taskID, *contextID, *returnImmediately); err != nil {
		log.Fatal(err)
	}
}

func validateProfile(card *a2a.AgentCard) error {
	for _, endpoint := range card.SupportedInterfaces {
		if endpoint.ProtocolBinding == a2a.TransportProtocolJSONRPC && endpoint.ProtocolVersion == a2a.Version {
			return nil
		}
	}
	return errors.New("no JSONRPC interface with protocolVersion 1.0")
}

func run(
	ctx context.Context,
	encoder *json.Encoder,
	client *a2aclient.Client,
	operation string,
	text string,
	taskID string,
	contextID string,
	returnImmediately bool,
) error {
	switch operation {
	case "send":
		result, err := sendMessageWithRetry(ctx, client, sendRequest(text, taskID, contextID, returnImmediately))
		if err != nil {
			return fmt.Errorf("send message: %w", err)
		}
		return writeOutput(encoder, outputEnvelope{Kind: eventKind(result), Event: result})
	case "stream":
		for event, err := range client.SendStreamingMessage(ctx, sendRequest(text, taskID, contextID, returnImmediately)) {
			if err != nil {
				return fmt.Errorf("stream message: %w", err)
			}
			if err := writeOutput(encoder, outputEnvelope{Kind: eventKind(event), Event: event}); err != nil {
				return err
			}
		}
		return nil
	case "get":
		if taskID == "" {
			return errors.New("--task-id is required for get")
		}
		task, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)})
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		return writeOutput(encoder, outputEnvelope{Kind: eventKind(task), Event: task})
	case "cancel":
		if taskID == "" {
			return errors.New("--task-id is required for cancel")
		}
		task, err := client.CancelTask(ctx, &a2a.CancelTaskRequest{ID: a2a.TaskID(taskID)})
		if err != nil {
			return fmt.Errorf("cancel task: %w", err)
		}
		return writeOutput(encoder, outputEnvelope{Kind: eventKind(task), Event: task})
	case "subscribe":
		if taskID == "" {
			return errors.New("--task-id is required for subscribe")
		}
		for event, err := range client.SubscribeToTask(ctx, &a2a.SubscribeToTaskRequest{ID: a2a.TaskID(taskID)}) {
			if err != nil {
				return fmt.Errorf("subscribe to task: %w", err)
			}
			if err := writeOutput(encoder, outputEnvelope{Kind: eventKind(event), Event: event}); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown --operation %q", operation)
	}
}

// A2A servers may briefly retain an interrupted execution while their local
// execution manager releases it. Retry only an explicitly named continuation
// and only for that transient admission error; a new Task or an ambiguous
// transport failure must never be replayed by this probe.
func sendMessageWithRetry(ctx context.Context, client *a2aclient.Client, request *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	if client == nil || request == nil {
		return nil, errors.New("A2A client and request are required")
	}
	return retryContinuation(ctx, request, func() (a2a.SendMessageResult, error) {
		return client.SendMessage(ctx, request)
	}, 20, 10*time.Millisecond, 250*time.Millisecond)
}

func retryContinuation(
	ctx context.Context,
	request *a2a.SendMessageRequest,
	send func() (a2a.SendMessageResult, error),
	maxAttempts int,
	initialBackoff time.Duration,
	maxBackoff time.Duration,
) (a2a.SendMessageResult, error) {
	if ctx == nil {
		return nil, errors.New("continuation retry context is required")
	}
	if request == nil || send == nil {
		return nil, errors.New("continuation retry request and sender are required")
	}
	if maxAttempts < 1 {
		return nil, errors.New("continuation retry maxAttempts must be positive")
	}
	if initialBackoff < 0 || maxBackoff < 0 || maxBackoff < initialBackoff {
		return nil, errors.New("continuation retry backoff is invalid")
	}
	backoff := initialBackoff
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := send()
		if err == nil || request.Message == nil || request.Message.TaskID == "" || !strings.Contains(strings.ToLower(err.Error()), "task execution is already in progress") || attempt == maxAttempts {
			return result, err
		}
		if backoff == 0 {
			continue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return nil, errors.New("A2A continuation retry exhausted")
}

func sendRequest(text string, taskID string, contextID string, returnImmediately bool) *a2a.SendMessageRequest {
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	message.TaskID = a2a.TaskID(taskID)
	message.ContextID = contextID
	return &a2a.SendMessageRequest{
		Message: message,
		Config: &a2a.SendMessageConfig{
			AcceptedOutputModes: []string{"text/plain", "application/json", "application/octet-stream"},
			ReturnImmediately:   returnImmediately,
		},
	}
}

func eventKind(event a2a.Event) string {
	switch event.(type) {
	case *a2a.Message:
		return "message"
	case *a2a.Task:
		return "task"
	case *a2a.TaskStatusUpdateEvent:
		return "status-update"
	case *a2a.TaskArtifactUpdateEvent:
		return "artifact-update"
	default:
		return "unknown"
	}
}

func writeOutput(encoder *json.Encoder, output outputEnvelope) error {
	if err := encoder.Encode(output); err != nil {
		if errors.Is(err, io.ErrClosedPipe) {
			return nil
		}
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}
