package aampfederation

import (
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestObservationFromMailMapsLifecycleAndArtifacts(t *testing.T) {
	tests := []struct {
		intent string
		status string
		state  core.TaskState
	}{
		{intent: "task.dispatch", state: core.TaskStateSubmitted},
		{intent: "task.ack", state: core.TaskStateWorking},
		{intent: "task.help_needed", state: core.TaskStateInputRequired},
		{intent: "task.cancel", state: core.TaskStateUnknown},
		{intent: "task.result", status: "completed", state: core.TaskStateCompleted},
		{intent: "task.result", status: "rejected", state: core.TaskStateRejected},
	}
	for _, test := range tests {
		t.Run(test.intent+test.status, func(t *testing.T) {
			observation, err := ObservationFromMail(MailEvent{
				Version: Version, Intent: test.intent, Status: test.status,
				TaskID: "remote-1", MessageID: "message-1", Body: "result",
				StructuredResult: []any{"one", 2.0},
				Attachments:      []Attachment{{ID: "file-1", Filename: "report.txt", URI: "jmap://blob-1"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.State != test.state || observation.RemoteTaskID != "remote-1" {
				t.Fatalf("observation=%+v", observation)
			}
			if test.intent == "task.cancel" && !observation.CancelRequested {
				t.Fatal("AAMP cancellation intent was not preserved")
			}
			if test.intent == "task.result" && len(observation.Artifacts) != 1 {
				t.Fatalf("result artifacts=%+v", observation.Artifacts)
			}
		})
	}
}

func TestObservationFromMailRejectsWrongVersion(t *testing.T) {
	if _, err := ObservationFromMail(MailEvent{
		Version: "1.0", Intent: "task.ack", TaskID: "task-1", MessageID: "message-1",
	}); err == nil {
		t.Fatal("unsupported version accepted")
	}
}
