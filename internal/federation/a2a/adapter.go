package a2afederation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"sort"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

type Adapter struct {
	resolver        *agentcard.Resolver
	transportClient *http.Client
	secrets         secrets.Provider
	profiles        []BindingProfile
}

func New(timeout time.Duration, providers ...secrets.Provider) *Adapter {
	client := &http.Client{Timeout: timeout}
	adapter := &Adapter{resolver: &agentcard.Resolver{Client: client}, transportClient: client,
		profiles: []BindingProfile{InitialBindingProfile}}
	if len(providers) > 0 {
		adapter.secrets = providers[0]
	}
	return adapter
}

// NewWithProfiles creates an adapter with an explicit ordered set of A2A wire
// profiles. The default constructor intentionally exposes only the accepted
// first profile; callers must opt into additional Bindings and test them.
func NewWithProfiles(timeout time.Duration, profiles []BindingProfile, providers ...secrets.Provider) (*Adapter, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("at least one A2A binding profile is required")
	}
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			return nil, err
		}
	}
	client := &http.Client{Timeout: timeout}
	adapter := &Adapter{resolver: &agentcard.Resolver{Client: client}, transportClient: client, profiles: append([]BindingProfile(nil), profiles...)}
	if len(providers) > 0 {
		adapter.secrets = providers[0]
	}
	return adapter, nil
}

func (a *Adapter) Discover(ctx context.Context, cardURL string) (federation.Descriptor, error) {
	card, err := a.resolver.Resolve(ctx, cardURL)
	if err != nil {
		return federation.Descriptor{}, mapError(err, false)
	}
	endpoint, _, err := selectEndpointForProfiles(card, a.profiles)
	if err != nil {
		return federation.Descriptor{}, profileSelectionError(err)
	}
	schemes := make([]string, 0, len(card.SecuritySchemes))
	for name := range card.SecuritySchemes {
		schemes = append(schemes, string(name))
	}
	sort.Strings(schemes)
	skills := make([]string, 0, len(card.Skills))
	for _, skill := range card.Skills {
		skills = append(skills, string(skill.ID))
	}
	sort.Strings(skills)
	return federation.Descriptor{
		Name: card.Name, ProviderVersion: card.Version,
		ProtocolBinding: string(normalizeBinding(string(endpoint.ProtocolBinding))),
		ProtocolVersion: string(endpoint.ProtocolVersion), Endpoint: endpoint.URL,
		Streaming:         card.Capabilities.Streaming,
		PushNotifications: card.Capabilities.PushNotifications,
		SecuritySchemes:   schemes, Skills: skills,
	}, nil
}

func selectEndpoint(card *a2a.AgentCard) (*a2a.AgentInterface, error) {
	endpoint, _, err := selectEndpointForProfiles(card, []BindingProfile{InitialBindingProfile})
	if err == nil {
		return endpoint, nil
	}
	return nil, &federation.Error{Problem: core.Problem{
		Category: "protocol", Code: "VERSION_NOT_SUPPORTED",
		Message: "Agent Card has no JSONRPC interface with protocol version 1.0",
	}, Cause: a2a.ErrVersionNotSupported}
}

func (a *Adapter) client(ctx context.Context, agent core.Agent) (*a2aclient.Client, context.Context, error) {
	card, err := a.resolver.Resolve(ctx, agent.CardURL)
	if err != nil {
		return nil, ctx, mapError(err, false)
	}
	normalizeCardBindings(card)
	endpoint, profile, err := selectEndpointForProfiles(card, a.profiles)
	if err != nil {
		return nil, ctx, profileSelectionError(err)
	}
	if endpoint.URL != agent.Endpoint || string(normalizeBinding(string(endpoint.ProtocolBinding))) != agent.ProtocolBinding ||
		string(endpoint.ProtocolVersion) != agent.ProtocolVersion {
		return nil, ctx, &federation.Error{Problem: core.Problem{
			Category: "protocol", Code: "AGENT_INTERFACE_CHANGED",
			Message: "Agent Card interface changed after registration",
		}}
	}

	credentials := a2aclient.NewInMemoryCredentialsStore()
	sessionID := a2aclient.SessionID(agent.ID)
	for scheme, reference := range agent.CredentialEnv {
		if a.secrets == nil {
			return nil, ctx, &federation.Error{Problem: core.Problem{
				Category: "authentication", Code: "SECRET_PROVIDER_REQUIRED",
				Message: "remote Agent credential provider is not configured",
			}}
		}
		value, err := a.secrets.Resolve(ctx, reference)
		if err != nil {
			return nil, ctx, &federation.Error{Problem: core.Problem{
				Category: "authentication", Code: "CREDENTIAL_UNAVAILABLE",
				Message: "remote Agent credential is unavailable",
			}, Cause: err}
		}
		credentials.Set(sessionID, a2a.SecuritySchemeName(scheme), a2aclient.AuthCredential(value))
	}
	if err := validateCredentials(ctx, card, credentials, sessionID); err != nil {
		return nil, ctx, err
	}

	options := []a2aclient.FactoryOption{
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithConfig(a2aclient.Config{
			PreferredTransports:      []a2a.TransportProtocol{profile.Binding},
			DisableTenantPropagation: true,
		}),
		a2aclient.WithCallInterceptors(&a2aclient.AuthInterceptor{Service: credentials}),
	}
	if profile.Binding == a2a.TransportProtocolJSONRPC {
		options = append(options, a2aclient.WithJSONRPCTransport(a.transportClient))
	} else {
		options = append(options, a2aclient.WithRESTTransport(a.transportClient))
	}
	client, err := a2aclient.NewFromCard(ctx, card, options...)
	if err != nil {
		return nil, ctx, mapError(err, false)
	}
	return client, a2aclient.AttachSessionID(ctx, sessionID), nil
}

func profileSelectionError(cause error) *federation.Error {
	return &federation.Error{Problem: core.Problem{
		Category: "protocol", Code: "VERSION_OR_BINDING_NOT_SUPPORTED",
		Message: "Agent Card does not advertise a configured A2A binding profile",
	}, Cause: cause}
}

func validateCredentials(
	ctx context.Context,
	card *a2a.AgentCard,
	credentials a2aclient.CredentialsService,
	sessionID a2aclient.SessionID,
) error {
	if len(card.SecurityRequirements) == 0 {
		return nil
	}
	sawSingleSchemeOption := false
	for _, option := range card.SecurityRequirements {
		if len(option) == 0 {
			return nil
		}
		// The SDK interceptor currently emits one credential per call. Refuse an
		// AND requirement instead of claiming authentication it cannot send.
		if len(option) > 1 {
			continue
		}
		sawSingleSchemeOption = true
		for scheme := range option {
			if _, err := credentials.Get(ctx, sessionID, scheme); err == nil {
				return nil
			}
		}
	}
	if !sawSingleSchemeOption {
		return &federation.Error{Problem: core.Problem{
			Category: "protocol", Code: "COMPOUND_AUTH_NOT_SUPPORTED",
			Message: "Agent Card requires a compound authentication scheme that this adapter cannot send",
		}, Cause: a2a.ErrUnsupportedOperation}
	}
	return &federation.Error{Problem: core.Problem{
		Category: "authentication", Code: "CREDENTIAL_REQUIRED",
		Message: "Agent Card requires a credential that is not configured",
	}, Cause: a2a.ErrUnauthenticated}
}

func (a *Adapter) Send(ctx context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		client, callCtx, err := a.client(ctx, agent)
		if err != nil {
			yield(federation.Observation{}, err)
			return
		}
		defer client.Destroy()
		requestMessage := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(message.Text))
		requestMessage.ID = message.ID
		requestMessage.TaskID = a2a.TaskID(message.RemoteTaskID)
		requestMessage.ContextID = message.RemoteContextID
		config := &a2a.SendMessageConfig{
			AcceptedOutputModes: []string{"text/plain", "application/json", "application/octet-stream"},
			ReturnImmediately:   message.ReturnImmediately,
		}
		if message.Push != nil {
			config.PushConfig = &a2a.PushConfig{
				URL: message.Push.URL, Token: message.Push.Token,
				Auth: &a2a.PushAuthInfo{Scheme: "Bearer", Credentials: message.Push.Token},
			}
		}
		request := &a2a.SendMessageRequest{Message: requestMessage, Config: config}
		if message.ReturnImmediately {
			// A non-streaming SendMessage is the portable A2A fast-ack path. Some
			// providers keep a streaming response open for the entire workflow even
			// when ReturnImmediately is set; using SendMessage here lets the Hub
			// acknowledge the remote Task and reconcile it asynchronously.
			result, sendErr := client.SendMessage(callCtx, request)
			if sendErr != nil {
				yield(federation.Observation{}, mapError(sendErr, false))
				return
			}
			observation, convertErr := observationFromEvent(result)
			if convertErr != nil {
				yield(federation.Observation{}, convertErr)
				return
			}
			yield(observation, nil)
			return
		}
		for event, eventErr := range client.SendStreamingMessage(callCtx, request) {
			if eventErr != nil {
				yield(federation.Observation{}, mapError(eventErr, true))
				return
			}
			observation, convertErr := observationFromEvent(event)
			if convertErr != nil {
				yield(federation.Observation{}, convertErr)
				return
			}
			if !yield(observation, nil) {
				return
			}
		}
	}
}

func DecodePush(payload []byte) (federation.Observation, error) {
	var response a2a.StreamResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return federation.Observation{}, &federation.Error{Problem: core.Problem{
			Category: "validation", Code: "INVALID_PUSH_PAYLOAD",
			Message: "Push payload is not a valid A2A StreamResponse",
		}, Cause: err}
	}
	return observationFromEvent(response.Event)
}

func (a *Adapter) Get(ctx context.Context, agent core.Agent, remoteTaskID string) (federation.Observation, error) {
	client, callCtx, err := a.client(ctx, agent)
	if err != nil {
		return federation.Observation{}, err
	}
	defer client.Destroy()
	task, err := client.GetTask(callCtx, &a2a.GetTaskRequest{ID: a2a.TaskID(remoteTaskID)})
	if err != nil {
		return federation.Observation{}, mapError(err, false)
	}
	return observationFromEvent(task)
}

func (a *Adapter) Cancel(ctx context.Context, agent core.Agent, remoteTaskID string) (federation.Observation, error) {
	client, callCtx, err := a.client(ctx, agent)
	if err != nil {
		return federation.Observation{}, err
	}
	defer client.Destroy()
	task, err := client.CancelTask(callCtx, &a2a.CancelTaskRequest{ID: a2a.TaskID(remoteTaskID)})
	if err != nil {
		return federation.Observation{}, mapError(err, false)
	}
	return observationFromEvent(task)
}

func (a *Adapter) Subscribe(ctx context.Context, agent core.Agent, remoteTaskID string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		client, callCtx, err := a.client(ctx, agent)
		if err != nil {
			yield(federation.Observation{}, err)
			return
		}
		defer client.Destroy()
		request := &a2a.SubscribeToTaskRequest{ID: a2a.TaskID(remoteTaskID)}
		for event, eventErr := range client.SubscribeToTask(callCtx, request) {
			if eventErr != nil {
				yield(federation.Observation{}, mapError(eventErr, true))
				return
			}
			observation, convertErr := observationFromEvent(event)
			if convertErr != nil {
				yield(federation.Observation{}, convertErr)
				return
			}
			if !yield(observation, nil) {
				return
			}
		}
	}
}

func observationFromEvent(event a2a.Event) (federation.Observation, error) {
	observation := federation.Observation{
		Source: "a2a", DedupKey: "a2a:" + core.DigestJSON(event), State: core.TaskStateUnknown,
	}
	switch value := event.(type) {
	case *a2a.Message:
		observation.State = core.TaskStateCompleted
		observation.Final = true
		observation.RemoteContextID = value.ContextID
		observation.Artifacts = []federation.ArtifactUpdate{{Artifact: core.Artifact{
			ID: "message:" + value.ID, Name: "direct-message",
			Parts: convertParts(value.Parts), Complete: true,
		}}}
	case *a2a.Task:
		observation.RemoteTaskID = string(value.ID)
		observation.RemoteContextID = value.ContextID
		observation.State = convertState(value.Status.State)
		observation.Final = observation.State.Terminal()
		observation.RemoteObservedAt = value.Status.Timestamp
		for _, artifact := range value.Artifacts {
			observation.Artifacts = append(observation.Artifacts, federation.ArtifactUpdate{
				Artifact: convertArtifact(artifact, true),
			})
		}
	case *a2a.TaskStatusUpdateEvent:
		observation.RemoteTaskID = string(value.TaskID)
		observation.RemoteContextID = value.ContextID
		observation.State = convertState(value.Status.State)
		observation.Final = observation.State.Terminal()
		observation.RemoteObservedAt = value.Status.Timestamp
	case *a2a.TaskArtifactUpdateEvent:
		observation.RemoteTaskID = string(value.TaskID)
		observation.RemoteContextID = value.ContextID
		observation.Artifacts = []federation.ArtifactUpdate{{
			Artifact: convertArtifact(value.Artifact, value.LastChunk), Append: value.Append,
		}}
	default:
		return federation.Observation{}, &federation.Error{Problem: core.Problem{
			Category: "protocol", Code: "INVALID_AGENT_RESPONSE",
			Message: "remote Agent returned an unsupported event",
		}, Cause: a2a.ErrInvalidAgentResponse}
	}
	return observation, nil
}

func convertArtifact(value *a2a.Artifact, complete bool) core.Artifact {
	if value == nil {
		return core.Artifact{ID: "missing", Complete: complete}
	}
	return core.Artifact{
		ID: string(value.ID), Name: value.Name, Description: value.Description,
		Parts: convertParts(value.Parts), Complete: complete,
	}
}

func convertParts(parts a2a.ContentParts) []core.Part {
	result := make([]core.Part, 0, len(parts))
	for _, part := range parts {
		converted := core.Part{MediaType: part.MediaType, Filename: part.Filename}
		switch value := part.Content.(type) {
		case a2a.Text:
			converted.Kind = core.PartText
			converted.Text = string(value)
		case a2a.Raw:
			converted.Kind = core.PartFile
			converted.BytesBase64 = base64.StdEncoding.EncodeToString(value)
		case a2a.URL:
			converted.Kind = core.PartFile
			converted.URI = string(value)
		case a2a.Data:
			converted.Kind = core.PartData
			encoded, _ := json.Marshal(value.Value)
			_ = json.Unmarshal(encoded, &converted.Data)
		}
		result = append(result, converted)
	}
	return result
}

func convertState(state a2a.TaskState) core.TaskState {
	switch state {
	case a2a.TaskStateSubmitted:
		return core.TaskStateSubmitted
	case a2a.TaskStateWorking:
		return core.TaskStateWorking
	case a2a.TaskStateInputRequired:
		return core.TaskStateInputRequired
	case a2a.TaskStateAuthRequired:
		return core.TaskStateAuthRequired
	case a2a.TaskStateCompleted:
		return core.TaskStateCompleted
	case a2a.TaskStateFailed:
		return core.TaskStateFailed
	case a2a.TaskStateCanceled:
		return core.TaskStateCanceled
	case a2a.TaskStateRejected:
		return core.TaskStateRejected
	default:
		return core.TaskStateUnknown
	}
}

func mapError(err error, ambiguous bool) error {
	problem := core.Problem{
		Category: "transport", Code: "REMOTE_TRANSPORT_ERROR",
		Message: "remote Agent request failed", Retryable: true, Ambiguous: ambiguous,
	}
	switch {
	case errors.Is(err, a2a.ErrVersionNotSupported):
		problem = core.Problem{Category: "protocol", Code: "VERSION_NOT_SUPPORTED", Message: "remote Agent rejected the A2A version"}
	case errors.Is(err, a2a.ErrUnauthenticated):
		problem = core.Problem{Category: "authentication", Code: "UNAUTHENTICATED", Message: "remote Agent authentication failed"}
	case errors.Is(err, a2a.ErrUnauthorized):
		problem = core.Problem{Category: "authorization", Code: "UNAUTHORIZED", Message: "remote Agent denied the operation"}
	case errors.Is(err, a2a.ErrTaskNotFound):
		problem = core.Problem{Category: "resource", Code: "TASK_NOT_FOUND", Message: "remote task is unavailable"}
	case errors.Is(err, a2a.ErrTaskNotCancelable):
		problem = core.Problem{Category: "state", Code: "TASK_NOT_CANCELABLE", Message: "remote task is not cancelable"}
	case errors.Is(err, a2a.ErrInvalidParams), errors.Is(err, a2a.ErrUnsupportedContentType):
		problem = core.Problem{Category: "validation", Code: a2a.ErrorReason(err), Message: "remote Agent rejected the request"}
	case errors.Is(err, a2a.ErrUnsupportedOperation), errors.Is(err, a2a.ErrPushNotificationNotSupported):
		problem = core.Problem{Category: "protocol", Code: a2a.ErrorReason(err), Message: "remote Agent does not support the operation"}
	}
	return &federation.Error{Problem: problem, Cause: fmt.Errorf("a2a call: %w", err)}
}
