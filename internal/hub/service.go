package hub

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

type Service struct {
	Store                 core.Store
	Adapter               federation.Adapter
	PublicBaseURL         string
	AllowPrivateAgentURLs bool
	Secrets               secrets.Provider
	Artifacts             *artifactstore.Service
	Now                   func() time.Time
	TokenGenerator        func() (string, error)
}

type RegisterAgentInput struct {
	ID            string            `json:"id,omitempty"`
	CardURL       string            `json:"cardUrl"`
	CredentialEnv map[string]string `json:"credentialEnv,omitempty"`
	// RegistrationSource and RegistryEndpoint are Hub-internal provenance
	// fields used by external Registry synchronization.
	RegistrationSource string `json:"-"`
	RegistryEndpoint   string `json:"-"`
}

// AgentRegistrationPolicy contains local constraints that a discovered
// Agent Card must satisfy before the Agent is persisted. The remote Agent
// Card remains authoritative for its endpoint and declared capabilities.
type AgentRegistrationPolicy struct {
	RequiredProtocolVersion  string
	RequiredProtocolBinding  string
	RequiredStreamTransport  string
	RequireStreaming         bool
	RequirePushNotifications bool
	RequiredSkills           []string
	AllowedSkills            []string
}

type SubmitTaskInput struct {
	AgentID    string `json:"agentId,omitempty"`
	Skill      string `json:"skill,omitempty"`
	Text       string `json:"text"`
	EnablePush bool   `json:"enablePush,omitempty"`
}

// ResolveAgent keeps explicit agentId calls compatible while allowing callers
// to route by an AgentCard-declared skill within their tenant.
func (s *Service) ResolveAgent(ctx context.Context, tenantID, agentID, skill string) (core.Agent, error) {
	if strings.TrimSpace(agentID) != "" {
		agent, err := s.Store.GetAgent(ctx, tenantID, agentID)
		if err != nil {
			return core.Agent{}, err
		}
		if strings.TrimSpace(skill) != "" && !hasSkill(agent, skill) {
			return core.Agent{}, fmt.Errorf("Agent %q does not declare skill %q", agentID, skill)
		}
		if agent.HealthStatus == core.AgentHealthUnhealthy || agent.HealthStatus == core.AgentHealthStale {
			return core.Agent{}, fmt.Errorf("Agent %q is unhealthy: %s", agentID, agent.HealthMessage)
		}
		return agent, nil
	}
	if strings.TrimSpace(skill) == "" {
		return core.Agent{}, errors.New("agent ID or skill is required")
	}
	agents, err := s.Store.ListAgents(ctx, tenantID)
	if err != nil {
		return core.Agent{}, err
	}
	for _, agent := range agents {
		if agent.HealthStatus != core.AgentHealthUnhealthy && agent.HealthStatus != core.AgentHealthStale && hasSkill(agent, skill) {
			return agent, nil
		}
	}
	return core.Agent{}, fmt.Errorf("no healthy Agent declares skill %q", skill)
}

func hasSkill(agent core.Agent, skill string) bool {
	for _, declared := range agent.Skills {
		if declared == skill {
			return true
		}
	}
	return false
}

// RefreshAgent re-resolves the public AgentCard and persists its current
// endpoint/capabilities. A failed refresh marks the registration unhealthy.
func (s *Service) RefreshAgent(ctx context.Context, tenantID, agentID string) (core.Agent, error) {
	agent, err := s.Store.GetAgent(ctx, tenantID, agentID)
	if err != nil {
		return core.Agent{}, err
	}
	now := s.now()
	descriptor, discoverErr := s.Adapter.Discover(ctx, agent.CardURL)
	if discoverErr != nil {
		agent.HealthStatus = core.AgentHealthUnhealthy
		agent.HealthMessage = discoverErr.Error()
		agent.LastHealthCheckAt = &now
		agent.UpdatedAt = now
		_ = s.Store.PutAgent(ctx, agent)
		return agent, discoverErr
	}
	if err := validateAgentEndpoint(descriptor.ProtocolBinding, descriptor.Endpoint, !s.AllowPrivateAgentURLs); err != nil {
		return agent, fmt.Errorf("Agent endpoint URL: %w", err)
	}
	agent.Name = descriptor.Name
	agent.ProviderVersion = descriptor.ProviderVersion
	agent.ProtocolBinding = descriptor.ProtocolBinding
	agent.ProtocolVersion = descriptor.ProtocolVersion
	agent.Endpoint = descriptor.Endpoint
	agent.Streaming = descriptor.Streaming
	agent.PushNotifications = descriptor.PushNotifications
	agent.SecuritySchemes = descriptor.SecuritySchemes
	agent.CardSignatureVerified = descriptor.CardSignatureVerified
	agent.CardSignatureKeyID = descriptor.CardSignatureKeyID
	agent.Skills = descriptor.Skills
	agent.HealthStatus = core.AgentHealthHealthy
	agent.HealthMessage = ""
	agent.LastHealthCheckAt = &now
	agent.UpdatedAt = now
	if err := s.Store.PutAgent(ctx, agent); err != nil {
		return core.Agent{}, err
	}
	return agent, nil
}

type ContinueTaskInput struct {
	Text string `json:"text"`
}

type RevokeTokenInput struct {
	Issuer    string    `json:"issuer"`
	TokenID   string    `json:"tokenId"`
	ExpiresAt time.Time `json:"expiresAt"`
	Reason    string    `json:"reason,omitempty"`
}

func (s *Service) RegisterAgent(ctx context.Context, tenantID string, input RegisterAgentInput) (core.Agent, error) {
	return s.RegisterAgentWithPolicy(ctx, tenantID, input, AgentRegistrationPolicy{})
}

// ValidateAgentRegistration performs the complete remote Card and local
// policy check without mutating the Store. Configuration controllers can use
// it to preflight every candidate before applying a new snapshot.
func (s *Service) ValidateAgentRegistration(ctx context.Context, tenantID string, input RegisterAgentInput, policy AgentRegistrationPolicy) (federation.Descriptor, error) {
	if tenantID == "" {
		return federation.Descriptor{}, errors.New("tenant ID is required")
	}
	if err := validateHTTPURL(input.CardURL, !s.AllowPrivateAgentURLs); err != nil {
		return federation.Descriptor{}, fmt.Errorf("card URL: %w", err)
	}
	descriptor, err := s.Adapter.Discover(ctx, input.CardURL)
	if err != nil {
		return federation.Descriptor{}, err
	}
	if policy.RequiredProtocolVersion != "" && descriptor.ProtocolVersion != policy.RequiredProtocolVersion {
		return federation.Descriptor{}, fmt.Errorf("remote Agent protocol version %q does not match required %q", descriptor.ProtocolVersion, policy.RequiredProtocolVersion)
	}
	if policy.RequiredProtocolBinding != "" && descriptor.ProtocolBinding != policy.RequiredProtocolBinding {
		return federation.Descriptor{}, fmt.Errorf("remote Agent protocol binding %q does not match required %q", descriptor.ProtocolBinding, policy.RequiredProtocolBinding)
	}
	if policy.RequiredStreamTransport != "" {
		stream := strings.ToUpper(strings.TrimSpace(policy.RequiredStreamTransport))
		binding := strings.ToUpper(strings.ReplaceAll(descriptor.ProtocolBinding, "_", ""))
		if (binding == "GRPC" && stream != "SERVER_STREAMING" && stream != "GRPC") ||
			(binding != "GRPC" && stream != "SSE") {
			return federation.Descriptor{}, fmt.Errorf("unsupported required stream transport %q for binding %q", policy.RequiredStreamTransport, descriptor.ProtocolBinding)
		}
	}
	if policy.RequireStreaming && !descriptor.Streaming {
		return federation.Descriptor{}, errors.New("remote Agent does not declare streaming support")
	}
	if policy.RequirePushNotifications && !descriptor.PushNotifications {
		return federation.Descriptor{}, errors.New("remote Agent does not declare Push support")
	}
	if missing := missingSkills(policy.RequiredSkills, descriptor.Skills); len(missing) > 0 {
		return federation.Descriptor{}, fmt.Errorf("remote Agent does not declare required skills: %s", strings.Join(missing, ", "))
	}
	if disallowed := disallowedSkills(policy.AllowedSkills, descriptor.Skills); len(disallowed) > 0 {
		return federation.Descriptor{}, fmt.Errorf("remote Agent declares skills outside the allowed policy: %s", strings.Join(disallowed, ", "))
	}
	if err := validateAgentEndpoint(descriptor.ProtocolBinding, descriptor.Endpoint, !s.AllowPrivateAgentURLs); err != nil {
		return federation.Descriptor{}, fmt.Errorf("Agent endpoint URL: %w", err)
	}
	declared := make(map[string]struct{}, len(descriptor.SecuritySchemes))
	for _, scheme := range descriptor.SecuritySchemes {
		declared[scheme] = struct{}{}
	}
	for scheme, envName := range input.CredentialEnv {
		if _, ok := declared[scheme]; !ok {
			return federation.Descriptor{}, fmt.Errorf("credential scheme %q is not declared by the Agent Card", scheme)
		}
		if s.Secrets == nil {
			return federation.Descriptor{}, errors.New("secret provider is required when credentials are configured")
		}
		if err := s.Secrets.ValidateReference(envName); err != nil {
			return federation.Descriptor{}, fmt.Errorf("credential reference %q: %w", envName, err)
		}
	}
	return descriptor, nil
}

func (s *Service) RegisterAgentWithPolicy(ctx context.Context, tenantID string, input RegisterAgentInput, policy AgentRegistrationPolicy) (core.Agent, error) {
	descriptor, err := s.ValidateAgentRegistration(ctx, tenantID, input, policy)
	if err != nil {
		return core.Agent{}, err
	}
	credentialEnv := make(map[string]string, len(input.CredentialEnv))
	for scheme, envName := range input.CredentialEnv {
		credentialEnv[scheme] = envName
	}
	now := s.now()
	id := input.ID
	if id == "" {
		id = core.NewID()
	}
	agent := core.Agent{
		ID: id, TenantID: tenantID, CardURL: input.CardURL,
		Name: descriptor.Name, ProviderVersion: descriptor.ProviderVersion,
		ProtocolBinding: descriptor.ProtocolBinding, ProtocolVersion: descriptor.ProtocolVersion,
		Endpoint: descriptor.Endpoint, Streaming: descriptor.Streaming,
		PushNotifications: descriptor.PushNotifications,
		SecuritySchemes:   descriptor.SecuritySchemes, CardSignatureVerified: descriptor.CardSignatureVerified,
		CardSignatureKeyID: descriptor.CardSignatureKeyID, Skills: descriptor.Skills,
		CredentialEnv:      credentialEnv,
		RegistrationSource: input.RegistrationSource,
		RegistryEndpoint:   input.RegistryEndpoint,
		HealthStatus:       core.AgentHealthHealthy,
		LastHealthCheckAt:  &now,
		CreatedAt:          now, UpdatedAt: now,
	}
	if err := s.Store.PutAgent(ctx, agent); err != nil {
		return core.Agent{}, err
	}
	return agent, nil
}

func missingSkills(required, declared []string) []string {
	set := make(map[string]struct{}, len(declared))
	for _, skill := range declared {
		set[skill] = struct{}{}
	}
	missing := make([]string, 0)
	for _, skill := range required {
		if _, ok := set[skill]; !ok {
			missing = append(missing, skill)
		}
	}
	return missing
}

func disallowedSkills(allowed, declared []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allowed))
	for _, skill := range allowed {
		set[skill] = struct{}{}
	}
	var result []string
	for _, skill := range declared {
		if _, ok := set[skill]; !ok {
			result = append(result, skill)
		}
	}
	return result
}

func (s *Service) ListAgents(ctx context.Context, tenantID string) ([]core.Agent, error) {
	return s.Store.ListAgents(ctx, tenantID)
}

func (s *Service) RevokeToken(ctx context.Context, tenantID string, input RevokeTokenInput) (core.TokenRevocation, error) {
	if tenantID == "" || strings.TrimSpace(input.Issuer) == "" || strings.TrimSpace(input.TokenID) == "" {
		return core.TokenRevocation{}, errors.New("tenant, issuer, and token ID are required")
	}
	if len(input.TokenID) > 512 || len(input.Issuer) > 2048 || len(input.Reason) > 512 {
		return core.TokenRevocation{}, errors.New("revocation input exceeds field limits")
	}
	now := s.now()
	if !input.ExpiresAt.After(now) {
		return core.TokenRevocation{}, errors.New("revocation expiry must be in the future")
	}
	store, ok := s.Store.(core.RevocationStore)
	if !ok {
		return core.TokenRevocation{}, errors.New("revocation store is not configured")
	}
	revocation := core.TokenRevocation{
		Issuer: strings.TrimSpace(input.Issuer), TokenID: strings.TrimSpace(input.TokenID),
		TenantID: tenantID, Reason: strings.TrimSpace(input.Reason),
		RevokedAt: now, ExpiresAt: input.ExpiresAt.UTC(),
	}
	if err := store.RevokeToken(ctx, revocation); err != nil {
		return core.TokenRevocation{}, err
	}
	return revocation, nil
}

func (s *Service) SubmitTask(ctx context.Context, tenantID string, input SubmitTaskInput) (core.Task, error) {
	if strings.TrimSpace(input.Text) == "" {
		return core.Task{}, errors.New("task text is required")
	}
	agent, err := s.ResolveAgent(ctx, tenantID, input.AgentID, input.Skill)
	if err != nil {
		return core.Task{}, err
	}
	now := s.now()
	task := core.Task{
		ID: core.NewID(), TenantID: tenantID, AgentID: agent.ID,
		MessageID: core.NewID(), InputDigest: core.DigestString(input.Text),
		State: core.TaskStateSubmitted, Delivery: core.DeliveryPending,
		CreatedAt: now, UpdatedAt: now,
	}
	var push *federation.PushConfig
	if input.EnablePush {
		if !agent.PushNotifications {
			return core.Task{}, errors.New("remote Agent does not declare Push support")
		}
		// Local fixtures may use loopback HTTP only when the explicit private-URL
		// development switch is enabled; all non-development callbacks remain
		// constrained to public HTTPS endpoints.
		if err := validateHTTPURL(s.PublicBaseURL, !s.AllowPrivateAgentURLs); err != nil {
			return core.Task{}, fmt.Errorf("public callback base URL: %w", err)
		}
		token, tokenErr := s.token()
		if tokenErr != nil {
			return core.Task{}, tokenErr
		}
		task.PushTokenHash = core.DigestString(token)
		push = &federation.PushConfig{
			URL: strings.TrimRight(s.PublicBaseURL, "/") + "/v1/tasks/" + url.PathEscape(task.ID) +
				"/push?tenant=" + url.QueryEscape(tenantID),
			Token: token,
		}
	}
	task, err = s.Store.CreateTask(ctx, task, core.Event{
		Type: "task.submitted", Source: "hub", State: task.State, CreatedAt: now,
	})
	if err != nil {
		return core.Task{}, err
	}
	task, _, err = s.Store.ApplyTask(ctx, tenantID, task.ID, "", func(current *core.Task) (core.Event, error) {
		// Once an outbound attempt starts, a process failure can leave its
		// outcome unknowable until a remote Task ID is observed.
		current.Delivery = core.DeliveryAmbiguous
		current.UpdatedAt = s.now()
		return core.Event{Type: "delivery.started", Source: "hub", CreatedAt: current.UpdatedAt}, nil
	})
	if err != nil {
		return task, err
	}

	message := federation.Message{ID: task.MessageID, Text: input.Text, Push: push, ReturnImmediately: true}
	var streamErr error
	for observation, observationErr := range s.Adapter.Send(ctx, agent, message) {
		if observationErr != nil {
			streamErr = observationErr
			break
		}
		task, err = s.applyObservation(ctx, task, observation)
		if err != nil {
			return task, err
		}
	}
	if streamErr == nil {
		return task, nil
	}

	problem := problemFromError(streamErr)
	if task.RemoteTaskID == "" {
		eventType := "delivery.ambiguous"
		if !problem.Ambiguous {
			eventType = "delivery.rejected"
		}
		task, _, err = s.Store.ApplyTask(ctx, tenantID, task.ID, "", func(current *core.Task) (core.Event, error) {
			if !problem.Ambiguous {
				current.Delivery = core.DeliveryPending
			}
			current.Problem = &problem
			current.UpdatedAt = s.now()
			return core.Event{Type: eventType, Source: "hub", Problem: &problem, CreatedAt: current.UpdatedAt}, nil
		})
		if err != nil {
			return task, err
		}
		if !problem.Ambiguous {
			return task, streamErr
		}
		return task, nil
	}

	// The provider acknowledged a Task. A broken stream is an observation failure,
	// not a Task failure; refresh the provider-owned Task instead of resending.
	reconciled, reconcileErr := s.ReconcileTask(ctx, tenantID, task.ID, true)
	if reconcileErr == nil {
		return reconciled, nil
	}
	task, _, err = s.Store.ApplyTask(ctx, tenantID, task.ID, "", func(current *core.Task) (core.Event, error) {
		current.Problem = &problem
		current.UpdatedAt = s.now()
		return core.Event{Type: "delivery.disconnected", Source: "hub", Problem: &problem, CreatedAt: current.UpdatedAt}, nil
	})
	return task, err
}

func (s *Service) GetTask(ctx context.Context, tenantID, taskID string) (core.Task, error) {
	return s.Store.GetTask(ctx, tenantID, taskID)
}

// ContinueTask sends a follow-up A2A Message on an existing provider-owned
// Task. The remote Task and Context IDs are retained by the Hub; callers only
// provide the new text and the Hub continues to reconcile the same Task.
func (s *Service) ContinueTask(ctx context.Context, tenantID, taskID string, input ContinueTaskInput) (core.Task, error) {
	if strings.TrimSpace(input.Text) == "" {
		return core.Task{}, errors.New("continuation text is required")
	}
	task, err := s.Store.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return core.Task{}, err
	}
	if task.RemoteTaskID == "" || task.RemoteContextID == "" {
		return task, errors.New("task has no remote Task and Context IDs")
	}
	if task.State != core.TaskStateInputRequired {
		return task, fmt.Errorf("task continuation requires INPUT_REQUIRED state, got %s", task.State)
	}
	agent, err := s.Store.GetAgent(ctx, tenantID, task.AgentID)
	if err != nil {
		return task, err
	}
	message := federation.Message{
		ID:                core.NewID(),
		Text:              input.Text,
		RemoteTaskID:      task.RemoteTaskID,
		RemoteContextID:   task.RemoteContextID,
		ReturnImmediately: true,
	}
	seen := false
	for observation, observationErr := range s.Adapter.Send(ctx, agent, message) {
		if observationErr != nil {
			return task, observationErr
		}
		seen = true
		task, err = s.applyObservation(ctx, task, observation)
		if err != nil {
			return task, err
		}
	}
	if !seen {
		return task, errors.New("remote Agent returned no continuation result")
	}
	return task, nil
}

func (s *Service) EventsAfter(ctx context.Context, tenantID, taskID string, after uint64) ([]core.Event, error) {
	return s.Store.EventsAfter(ctx, tenantID, taskID, after)
}

func (s *Service) CancelTask(ctx context.Context, tenantID, taskID string) (core.Task, error) {
	task, _, err := s.Store.ApplyTask(ctx, tenantID, taskID, "", func(current *core.Task) (core.Event, error) {
		current.CancelRequested = true
		current.UpdatedAt = s.now()
		return core.Event{Type: "task.cancel_requested", Source: "hub", CreatedAt: current.UpdatedAt}, nil
	})
	if err != nil {
		return core.Task{}, err
	}
	if task.RemoteTaskID == "" {
		return task, errors.New("task has no acknowledged remote task ID")
	}
	agent, err := s.Store.GetAgent(ctx, tenantID, task.AgentID)
	if err != nil {
		return task, err
	}
	observation, err := s.Adapter.Cancel(ctx, agent, task.RemoteTaskID)
	if err != nil {
		updated, recordErr := s.recordProblem(ctx, task, "task.cancel_unconfirmed", problemFromError(err))
		if recordErr != nil {
			return updated, recordErr
		}
		return updated, err
	}
	return s.applyObservation(ctx, task, observation)
}

func (s *Service) ReconcileTask(ctx context.Context, tenantID, taskID string, subscribe bool) (core.Task, error) {
	task, err := s.Store.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return core.Task{}, err
	}
	if task.RemoteTaskID == "" {
		return task, errors.New("ambiguous task has no remote task ID and cannot be automatically reconciled")
	}
	agent, err := s.Store.GetAgent(ctx, tenantID, task.AgentID)
	if err != nil {
		return task, err
	}
	observation, err := s.Adapter.Get(ctx, agent, task.RemoteTaskID)
	if err != nil {
		return task, err
	}
	task, err = s.applyObservation(ctx, task, observation)
	if err != nil || task.State.Terminal() || !subscribe || !agent.Streaming {
		return task, err
	}
	for observation, streamErr := range s.Adapter.Subscribe(ctx, agent, task.RemoteTaskID) {
		if streamErr != nil {
			return task, streamErr
		}
		task, err = s.applyObservation(ctx, task, observation)
		if err != nil || task.State.Terminal() {
			return task, err
		}
	}
	return task, nil
}

func (s *Service) Recover(ctx context.Context, subscribe bool) error {
	tasks, err := s.Store.ListRecoverable(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, task := range tasks {
		if _, err := s.ReconcileTask(ctx, task.TenantID, task.ID, subscribe); err != nil {
			failures = append(failures, fmt.Errorf("reconcile task %s: %w", task.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) AcceptPush(ctx context.Context, tenantID, taskID, token string, observation federation.Observation) (core.Task, error) {
	task, err := s.Store.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return core.Task{}, err
	}
	want := []byte(task.PushTokenHash)
	got := []byte(core.DigestString(token))
	if task.PushTokenHash == "" || subtle.ConstantTimeCompare(want, got) != 1 {
		return core.Task{}, ErrInvalidPushCredential
	}
	if observation.RemoteTaskID == "" ||
		(task.RemoteTaskID != "" && observation.RemoteTaskID != task.RemoteTaskID) {
		return core.Task{}, ErrPushTaskMismatch
	}
	observation.Source = "a2a-push"
	payload, err := json.Marshal(observation)
	if err != nil {
		return core.Task{}, fmt.Errorf("encode Push observation: %w", err)
	}
	inbox, ok := s.Store.(core.InboxStore)
	if !ok {
		return core.Task{}, ErrPushInboxUnavailable
	}
	dedupKey := observation.DedupKey
	if dedupKey == "" {
		dedupKey = "push:" + core.DigestJSON(observation)
	}
	_, err = inbox.EnqueueInbox(ctx, core.InboxItem{
		ID: core.NewID(), TenantID: tenantID, TaskID: taskID,
		DedupKey: dedupKey, Protocol: "a2a", Payload: payload, CreatedAt: s.now(),
	})
	return task, err
}

func (s *Service) ApplyInboxItem(ctx context.Context, item core.InboxItem) (core.Task, error) {
	if item.Protocol != "a2a" {
		return core.Task{}, fmt.Errorf("unsupported inbox protocol %q", item.Protocol)
	}
	var observation federation.Observation
	if err := json.Unmarshal(item.Payload, &observation); err != nil {
		return core.Task{}, fmt.Errorf("decode queued Push observation: %w", err)
	}
	task, err := s.Store.GetTask(ctx, item.TenantID, item.TaskID)
	if err != nil {
		return core.Task{}, err
	}
	if observation.RemoteTaskID == "" ||
		(task.RemoteTaskID != "" && observation.RemoteTaskID != task.RemoteTaskID) {
		return core.Task{}, ErrPushTaskMismatch
	}
	observation.Source = "a2a-push"
	return s.applyObservation(ctx, task, observation)
}

func (s *Service) applyObservation(ctx context.Context, task core.Task, observation federation.Observation) (core.Task, error) {
	if observation.State == "" {
		observation.State = core.TaskStateUnknown
	}
	baseKey := observation.DedupKey
	if baseKey == "" {
		baseKey = "observation:" + core.DigestJSON(observation)
	}
	for updateIndex := range observation.Artifacts {
		externalized, err := s.externalizeArtifact(
			ctx, task, observation.Artifacts[updateIndex].Artifact, baseKey, updateIndex,
		)
		if err != nil {
			return task, err
		}
		observation.Artifacts[updateIndex].Artifact = externalized
	}
	if observation.State != core.TaskStateUnknown || observation.RemoteTaskID != "" || observation.Problem != nil || observation.CancelRequested {
		var err error
		task, _, err = s.Store.ApplyTask(ctx, task.TenantID, task.ID, baseKey+":status", func(current *core.Task) (core.Event, error) {
			if observation.RemoteTaskID != "" {
				if current.RemoteTaskID != "" && current.RemoteTaskID != observation.RemoteTaskID {
					return core.Event{}, errors.New("remote task ID changed")
				}
				current.RemoteTaskID = observation.RemoteTaskID
			}
			if observation.RemoteContextID != "" {
				current.RemoteContextID = observation.RemoteContextID
			}
			current.Delivery = core.DeliveryAcknowledged
			if observation.CancelRequested {
				current.CancelRequested = true
			}
			applyRemoteState := shouldApplyState(*current, observation)
			if observation.State != core.TaskStateUnknown && applyRemoteState {
				current.State = observation.State
			}
			if observation.RemoteObservedAt != nil &&
				(current.LastRemoteObservedAt == nil || !observation.RemoteObservedAt.Before(*current.LastRemoteObservedAt)) {
				current.LastRemoteObservedAt = observation.RemoteObservedAt
			}
			current.Problem = observation.Problem
			current.UpdatedAt = s.now()
			return core.Event{
				Type: "task.status", Source: observation.Source, State: current.State,
				Problem: observation.Problem, ObservedAt: observation.RemoteObservedAt, CreatedAt: current.UpdatedAt,
			}, nil
		})
		if err != nil {
			return task, err
		}
	}
	for i, update := range observation.Artifacts {
		key := fmt.Sprintf("%s:artifact:%s:%d", baseKey, update.Artifact.ID, i)
		var err error
		task, _, err = s.Store.ApplyTask(ctx, task.TenantID, task.ID, key, func(current *core.Task) (core.Event, error) {
			mergeArtifact(current, update)
			current.UpdatedAt = s.now()
			artifact := update.Artifact
			return core.Event{Type: "task.artifact", Source: observation.Source, Artifact: &artifact, CreatedAt: current.UpdatedAt}, nil
		})
		if err != nil {
			return task, err
		}
	}
	return task, nil
}

func (s *Service) externalizeArtifact(
	ctx context.Context,
	task core.Task,
	value core.Artifact,
	dedupKey string,
	updateIndex int,
) (core.Artifact, error) {
	for partIndex := range value.Parts {
		part := &value.Parts[partIndex]
		if part.Kind != core.PartFile || part.ObjectID != "" || (part.BytesBase64 == "" && part.URI == "") {
			continue
		}
		if s.Artifacts == nil {
			return core.Artifact{}, errors.New("artifact object storage is not configured")
		}
		input := artifactstore.Input{
			TenantID: task.TenantID, TaskID: task.ID, ArtifactID: value.ID,
			DedupKey: fmt.Sprintf("%s:%d", dedupKey, updateIndex), PartIndex: partIndex,
			MediaType: part.MediaType, Filename: part.Filename,
		}
		var object core.ArtifactObject
		var err error
		if part.BytesBase64 != "" {
			object, err = s.Artifacts.IngestBase64(ctx, input, part.BytesBase64)
		} else {
			object, err = s.Artifacts.IngestURI(ctx, input, part.URI)
		}
		if err != nil {
			return core.Artifact{}, fmt.Errorf("externalize Artifact %q Part %d: %w", value.ID, partIndex, err)
		}
		part.BytesBase64 = ""
		part.URI = ""
		part.ObjectID = object.ID
		part.SizeBytes = object.SizeBytes
		part.SHA256 = object.SHA256
		part.MediaType = object.DetectedMediaType
	}
	return value, nil
}

func (s *Service) GetArtifact(ctx context.Context, tenantID, artifactID string) (core.ArtifactObject, error) {
	if s.Artifacts == nil {
		return core.ArtifactObject{}, errors.New("artifact object storage is not configured")
	}
	return s.Artifacts.Get(ctx, tenantID, artifactID)
}

func shouldApplyState(task core.Task, observation federation.Observation) bool {
	if task.State.Terminal() && task.State != observation.State {
		return false
	}
	if task.LastRemoteObservedAt != nil && observation.RemoteObservedAt != nil && observation.RemoteObservedAt.Before(*task.LastRemoteObservedAt) {
		return false
	}
	return true
}

func mergeArtifact(task *core.Task, update federation.ArtifactUpdate) {
	for i := range task.Artifacts {
		if task.Artifacts[i].ID != update.Artifact.ID {
			continue
		}
		if update.Append {
			task.Artifacts[i].Parts = append(task.Artifacts[i].Parts, update.Artifact.Parts...)
			task.Artifacts[i].Complete = update.Artifact.Complete
			if update.Artifact.Name != "" {
				task.Artifacts[i].Name = update.Artifact.Name
			}
			if update.Artifact.Description != "" {
				task.Artifacts[i].Description = update.Artifact.Description
			}
		} else {
			task.Artifacts[i] = update.Artifact
		}
		return
	}
	task.Artifacts = append(task.Artifacts, update.Artifact)
}

func (s *Service) recordProblem(ctx context.Context, task core.Task, eventType string, problem core.Problem) (core.Task, error) {
	updated, _, err := s.Store.ApplyTask(ctx, task.TenantID, task.ID, "", func(current *core.Task) (core.Event, error) {
		current.Problem = &problem
		current.UpdatedAt = s.now()
		return core.Event{Type: eventType, Source: "hub", Problem: &problem, CreatedAt: current.UpdatedAt}, nil
	})
	return updated, err
}

func problemFromError(err error) core.Problem {
	var adapterErr *federation.Error
	if errors.As(err, &adapterErr) {
		return adapterErr.Problem
	}
	return core.Problem{
		Category: "internal", Code: "FEDERATION_ERROR",
		Message: "federation operation failed", Retryable: false,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) token() (string, error) {
	if s.TokenGenerator != nil {
		return s.TokenGenerator()
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Push credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
