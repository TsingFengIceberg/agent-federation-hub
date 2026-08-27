package aampfederation

import (
	"errors"
	"fmt"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

const Version = "1.1"

type MailEvent struct {
	Version          string       `json:"version"`
	Intent           string       `json:"intent"`
	TaskID           string       `json:"taskId"`
	MessageID        string       `json:"messageId"`
	Body             string       `json:"body,omitempty"`
	Status           string       `json:"status,omitempty"`
	ErrorMessage     string       `json:"errorMessage,omitempty"`
	StructuredResult any          `json:"structuredResult,omitempty"`
	Attachments      []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType,omitempty"`
	URI       string `json:"uri"`
}

// ObservationFromMail maps the AAMP 1.1 lifecycle vocabulary onto the same
// provider-observation model used by A2A. SMTP/JMAP transport remains separate.
func ObservationFromMail(event MailEvent) (federation.Observation, error) {
	if event.Version != Version {
		return federation.Observation{}, fmt.Errorf("unsupported AAMP version %q", event.Version)
	}
	if event.TaskID == "" || event.MessageID == "" {
		return federation.Observation{}, errors.New("AAMP taskId and messageId are required")
	}
	observation := federation.Observation{
		DedupKey: "aamp:" + core.DigestJSON(event),
		Source:   "aamp", RemoteTaskID: event.TaskID, State: core.TaskStateUnknown,
	}
	switch event.Intent {
	case "task.dispatch":
		observation.State = core.TaskStateSubmitted
	case "task.ack":
		observation.State = core.TaskStateWorking
	case "task.help_needed":
		observation.State = core.TaskStateInputRequired
	case "task.cancel":
		observation.CancelRequested = true
	case "task.result":
		switch event.Status {
		case "completed":
			observation.State = core.TaskStateCompleted
		case "rejected":
			observation.State = core.TaskStateRejected
			observation.Problem = &core.Problem{
				Category: "remote", Code: "AAMP_REJECTED",
				Message: "remote AAMP executor rejected the task",
			}
		default:
			return federation.Observation{}, fmt.Errorf("unsupported AAMP result status %q", event.Status)
		}
	default:
		return federation.Observation{}, fmt.Errorf("unsupported AAMP task intent %q", event.Intent)
	}
	observation.Final = observation.State.Terminal()
	if event.Intent == "task.result" {
		parts := make([]core.Part, 0, 2+len(event.Attachments))
		if event.Body != "" {
			parts = append(parts, core.Part{Kind: core.PartText, MediaType: "text/plain", Text: event.Body})
		}
		if event.StructuredResult != nil {
			parts = append(parts, core.Part{Kind: core.PartData, MediaType: "application/json", Data: event.StructuredResult})
		}
		for _, attachment := range event.Attachments {
			parts = append(parts, core.Part{
				Kind: core.PartFile, Filename: attachment.Filename,
				MediaType: attachment.MediaType, URI: attachment.URI,
			})
		}
		if len(parts) > 0 {
			observation.Artifacts = []federation.ArtifactUpdate{{Artifact: core.Artifact{
				ID: "aamp-result:" + event.MessageID, Name: "AAMP task result",
				Parts: parts, Complete: true,
			}}}
		}
	}
	return observation, nil
}
