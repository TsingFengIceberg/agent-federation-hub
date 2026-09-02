package federation

import (
	"context"
	"iter"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type Descriptor struct {
	Name                  string
	ProviderVersion       string
	ProtocolBinding       string
	ProtocolVersion       string
	Endpoint              string
	Streaming             bool
	PushNotifications     bool
	SecuritySchemes       []string
	Skills                []string
	Extensions            []Extension
	CardSignatureVerified bool
	CardSignatureKeyID    string
}

type Extension struct {
	URI      string
	Required bool
}

type Message struct {
	ID                string
	Text              string
	RemoteTaskID      string
	RemoteContextID   string
	ReturnImmediately bool
	Push              *PushConfig
	// Extensions are the A2A extension URIs activated for this request. The
	// adapter must carry the same set in the service parameter and Message so
	// providers can enforce required-extension semantics consistently.
	Extensions []string
	// Metadata is opaque extension metadata. The Hub validates its size and
	// shape at the management boundary but never interprets provider semantics.
	Metadata map[string]any
}

type PushConfig struct {
	URL   string
	Token string
}

type ArtifactUpdate struct {
	Artifact core.Artifact
	Append   bool
}

type Observation struct {
	DedupKey         string
	Source           string
	RemoteTaskID     string
	RemoteContextID  string
	State            core.TaskState
	Artifacts        []ArtifactUpdate
	Problem          *core.Problem
	RemoteObservedAt *time.Time
	CancelRequested  bool
	Final            bool
}

type Adapter interface {
	Discover(context.Context, string) (Descriptor, error)
	Send(context.Context, core.Agent, Message) iter.Seq2[Observation, error]
	Get(context.Context, core.Agent, string) (Observation, error)
	Cancel(context.Context, core.Agent, string) (Observation, error)
	Subscribe(context.Context, core.Agent, string) iter.Seq2[Observation, error]
}

type Error struct {
	Problem core.Problem
	Cause   error
}

func (e *Error) Error() string {
	return e.Problem.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}
