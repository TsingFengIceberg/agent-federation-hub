// Package interop implements deterministic black-box Agent behavior for the
// repository-owned A2A interoperability fixture. It is not product Agent logic.
package interop

import (
	"context"
	"iter"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const (
	ScenarioMessage       = "message"
	ScenarioInputRequired = "input-required"
	ScenarioAuthRequired  = "auth-required"
	ScenarioLongRunning   = "long-running"
)

// ScenarioExecutor maps text commands to deterministic protocol behaviors.
type ScenarioExecutor struct{}

func (ScenarioExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		scenario := strings.TrimSpace(firstText(execCtx.Message))
		if scenario == ScenarioMessage {
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("fixture message response")), nil)
			return
		}

		if execCtx.StoredTask == nil && !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
			return
		}

		if scenario == ScenarioInputRequired {
			message := a2a.NewMessageForTask(
				a2a.MessageRoleAgent,
				execCtx,
				a2a.NewTextPart("fixture requires additional input"),
			)
			yield(statusUpdate(execCtx, a2a.TaskStateInputRequired, message), nil)
			return
		}
		if scenario == ScenarioAuthRequired {
			// AUTH_REQUIRED is a resumable protocol state. A follow-up Message
			// with the same Task/Context IDs is intentionally enough for this
			// deterministic fixture; a production Provider may require an
			// external OAuth/device-approval step before accepting it.
			if execCtx.StoredTask == nil || execCtx.StoredTask.Status.State != a2a.TaskStateAuthRequired {
				message := a2a.NewMessageForTask(
					a2a.MessageRoleAgent,
					execCtx,
					a2a.NewTextPart("fixture requires authorization"),
				)
				yield(statusUpdate(execCtx, a2a.TaskStateAuthRequired, message), nil)
				return
			}
		}

		if !yield(statusUpdate(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		if scenario == ScenarioLongRunning {
			<-ctx.Done()
			return
		}

		artifact := a2a.NewArtifactEvent(execCtx, artifactPart(scenario))
		artifact.LastChunk = true
		if !yield(artifact, nil) {
			return
		}
		yield(statusUpdate(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

func artifactPart(scenario string) *a2a.Part {
	switch scenario {
	case "artifact-file":
		part := a2a.NewRawPart([]byte("fixture file contents"))
		part.Filename = "fixture.txt"
		part.MediaType = "text/plain"
		return part
	case "artifact-file-url":
		part := a2a.NewFileURLPart(a2a.URL("https://example.invalid/fixture.txt"), "text/plain")
		part.Filename = "fixture.txt"
		return part
	case "artifact-data":
		return a2a.NewDataPart(map[string]any{"kind": "fixture", "ok": true})
	default:
		return a2a.NewTextPart("fixture task response: " + scenario)
	}
}

func (ScenarioExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(statusUpdate(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

func firstText(message *a2a.Message) string {
	if message == nil {
		return ""
	}
	for _, part := range message.Parts {
		if text := part.Text(); text != "" {
			return text
		}
	}
	return ""
}

func statusUpdate(info a2a.TaskInfoProvider, state a2a.TaskState, message *a2a.Message) *a2a.TaskStatusUpdateEvent {
	timestamp := time.Now().UTC()
	taskInfo := info.TaskInfo()
	return &a2a.TaskStatusUpdateEvent{
		TaskID:    taskInfo.TaskID,
		ContextID: taskInfo.ContextID,
		Status: a2a.TaskStatus{
			State:     state,
			Message:   message,
			Timestamp: &timestamp,
		},
	}
}
