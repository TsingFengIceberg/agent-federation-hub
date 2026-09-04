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
	"slices"
	"strings"
	"time"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/observability"
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
	Metrics               *observability.Metrics
	Tracer                observability.Tracer
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
	// RequiredProfiles is an ordered set of acceptable A2A interfaces. A
	// provider may advertise any one of these profiles; the first matching
	// profile is selected by the adapter. The legacy singular fields remain
	// supported for callers that predate profile negotiation.
	RequiredProfiles         []a2afederation.BindingProfile
	RequiredProtocolVersion  string
	RequiredProtocolBinding  string
	RequiredStreamTransport  string
	RequireStreaming         bool
	RequirePushNotifications bool
	RequiredSkills           []string
	AllowedSkills            []string
	RequiredExtensions       []string
	AllowedExtensions        []string
}

type SubmitTaskInput struct {
	AgentID        string         `json:"agentId,omitempty"`
	Skill          string         `json:"skill,omitempty"`
	Text           string         `json:"text,omitempty"`
	Parts          []core.Part    `json:"parts,omitempty"`
	EnablePush     bool           `json:"enablePush,omitempty"`
	Priority       int            `json:"priority,omitempty"`
	Extensions     []string       `json:"extensions,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
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
	Text  string      `json:"text,omitempty"`
	Parts []core.Part `json:"parts,omitempty"`
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
	descriptor, err := discoverWithRegistrationProfiles(ctx, s.Adapter, input.CardURL, policy.RequiredProfiles)
	if err != nil {
		return federation.Descriptor{}, err
	}
	if len(policy.RequiredProfiles) > 0 && !descriptorMatchesAnyProfile(descriptor, policy.RequiredProfiles) {
		return federation.Descriptor{}, fmt.Errorf("remote Agent does not advertise any configured A2A profile")
	}
	if policy.RequiredProtocolVersion != "" && !a2afederation.ProtocolVersionsCompatible(descriptor.ProtocolVersion, policy.RequiredProtocolVersion) {
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
	declaredExtensions := extensionURIs(descriptor.Extensions)
	if missing := missingSkills(policy.RequiredExtensions, declaredExtensions); len(missing) > 0 {
		return federation.Descriptor{}, fmt.Errorf("remote Agent does not declare required extensions: %s", strings.Join(missing, ", "))
	}
	if disallowed := disallowedSkills(policy.AllowedExtensions, declaredExtensions); len(disallowed) > 0 {
		return federation.Descriptor{}, fmt.Errorf("remote Agent declares extensions outside the allowed policy: %s", strings.Join(disallowed, ", "))
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

type profileAwareDiscovery interface {
	DiscoverWithProfiles(context.Context, string, []a2afederation.BindingProfile) (federation.Descriptor, error)
}

func discoverWithRegistrationProfiles(ctx context.Context, adapter federation.Adapter, cardURL string, profiles []a2afederation.BindingProfile) (federation.Descriptor, error) {
	if len(profiles) > 0 {
		if aware, ok := adapter.(profileAwareDiscovery); ok {
			return aware.DiscoverWithProfiles(ctx, cardURL, profiles)
		}
	}
	return adapter.Discover(ctx, cardURL)
}

func descriptorMatchesAnyProfile(descriptor federation.Descriptor, profiles []a2afederation.BindingProfile) bool {
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			continue
		}
		if !a2afederation.ProtocolVersionsCompatible(descriptor.ProtocolVersion, profile.ProtocolVersion) {
			continue
		}
		if normalizeBindingName(descriptor.ProtocolBinding) != normalizeBindingName(string(profile.Binding)) {
			continue
		}
		return true
	}
	return false
}

func normalizeBindingName(value string) string {
	return strings.ToUpper(strings.NewReplacer("_", "", "-", "", "+", "").Replace(strings.TrimSpace(value)))
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
		Extensions:         extensionURIs(descriptor.Extensions),
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

func extensionURIs(declared []federation.Extension) []string {
	result := make([]string, 0, len(declared))
	for _, extension := range declared {
		if value := strings.TrimSpace(extension.URI); value != "" {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func normalizeRequestExtensions(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, errors.New("A2A extension URI must not be empty")
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return nil, fmt.Errorf("invalid A2A extension URI %q", raw)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}

func validateExtensionMetadata(value map[string]any) (map[string]any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if len(value) > 64 {
		return nil, errors.New("extension metadata has too many keys")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("extension metadata must be JSON: %w", err)
	}
	if len(encoded) > 64<<10 {
		return nil, errors.New("extension metadata exceeds 64 KiB")
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, fmt.Errorf("extension metadata must be a JSON object: %w", err)
	}
	return clone, nil
}

func cloneAnyMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if json.Unmarshal(encoded, &clone) != nil {
		return nil
	}
	return clone
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
	parts, err := NormalizeMessageParts(input.Text, input.Parts)
	if err != nil {
		return core.Task{}, err
	}
	if input.Priority < -1000 || input.Priority > 1000 {
		return core.Task{}, errors.New("task priority must be between -1000 and 1000")
	}
	extensions, err := normalizeRequestExtensions(input.Extensions)
	if err != nil {
		return core.Task{}, err
	}
	metadata, err := validateExtensionMetadata(input.Metadata)
	if err != nil {
		return core.Task{}, err
	}
	agent, err := s.ResolveAgent(ctx, tenantID, input.AgentID, input.Skill)
	if err != nil {
		return core.Task{}, err
	}
	materializedParts, err := MaterializeMessageParts(ctx, tenantID, s.Artifacts, parts)
	if err != nil {
		return core.Task{}, err
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if len(idempotencyKey) > 256 {
		return core.Task{}, errors.New("task idempotency key exceeds 256 characters")
	}
	taskID, messageID := core.NewID(), core.NewID()
	if idempotencyKey != "" {
		stable := core.DigestString(tenantID + "\x00" + agent.ID + "\x00" + idempotencyKey)
		taskID, messageID = "task-"+stable[:32], "message-"+stable[32:]
	}
	now := s.now()
	task := core.Task{
		ID: taskID, TenantID: tenantID, AgentID: agent.ID,
		MessageID: messageID, IdempotencyKey: idempotencyKey, InputDigest: core.DigestJSON(struct {
			Parts      []core.Part    `json:"parts"`
			Extensions []string       `json:"extensions,omitempty"`
			Metadata   map[string]any `json:"metadata,omitempty"`
		}{parts, extensions, metadata}),
		RequestedExtensions: extensions, ExtensionMetadata: metadata,
		Priority: input.Priority,
		State:    core.TaskStateSubmitted, Delivery: core.DeliveryPending,
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
	proposedTask := task
	task, err = s.Store.CreateTask(ctx, task, core.Event{
		Type: "task.submitted", Source: "hub", State: task.State, CreatedAt: now,
	})
	if err != nil {
		if errors.Is(err, core.ErrConflict) && idempotencyKey != "" {
			existing, getErr := s.Store.GetTask(ctx, tenantID, proposedTask.ID)
			if getErr == nil && existing.IdempotencyKey == idempotencyKey && existing.InputDigest == proposedTask.InputDigest && existing.AgentID == proposedTask.AgentID {
				return existing, nil
			}
		}
		return core.Task{}, err
	}
	if s.Metrics != nil {
		s.Metrics.IncTaskSubmitted()
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

	message := federation.Message{ID: task.MessageID, Text: firstTextPart(parts), Parts: materializedParts, Push: push, ReturnImmediately: true,
		Extensions: append([]string(nil), task.RequestedExtensions...), Metadata: cloneAnyMap(task.ExtensionMetadata)}
	callContext := ctx
	var span observability.Span
	if s.Tracer != nil {
		callContext, span = s.Tracer.Start(ctx, "afh.task.submit", map[string]string{
			"afh.tenant_id": tenantID, "afh.task_id": task.ID, "afh.agent_id": agent.ID,
		})
	}
	var streamErr error
	if span != nil {
		defer func() { span.End(streamErr) }()
	}
	for observation, observationErr := range s.Adapter.Send(callContext, agent, message) {
		if observationErr != nil {
			streamErr = observationErr
			break
		}
		task, err = s.applyObservation(ctx, task, observation)
		if err != nil {
			streamErr = err
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
// provide the new text and the Hub continues to reconcile the same Task. Both
// INPUT_REQUIRED (human/data input) and AUTH_REQUIRED (fresh authorization or
// credential approval) are resumable states. Credential material itself is
// never accepted in this payload or persisted by the Hub.
func (s *Service) ContinueTask(ctx context.Context, tenantID, taskID string, input ContinueTaskInput) (core.Task, error) {
	parts, err := NormalizeMessageParts(input.Text, input.Parts)
	if err != nil {
		return core.Task{}, fmt.Errorf("continuation: %w", err)
	}
	task, err := s.Store.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return core.Task{}, err
	}
	if task.RemoteTaskID == "" || task.RemoteContextID == "" {
		return task, errors.New("task has no remote Task and Context IDs")
	}
	if task.State != core.TaskStateInputRequired && task.State != core.TaskStateAuthRequired {
		return task, fmt.Errorf("task continuation requires INPUT_REQUIRED or AUTH_REQUIRED state, got %s", task.State)
	}
	agent, err := s.Store.GetAgent(ctx, tenantID, task.AgentID)
	if err != nil {
		return task, err
	}
	materializedParts, err := MaterializeMessageParts(ctx, tenantID, s.Artifacts, parts)
	if err != nil {
		return task, err
	}
	message := federation.Message{
		ID:                core.NewID(),
		Text:              firstTextPart(parts),
		Parts:             materializedParts,
		RemoteTaskID:      task.RemoteTaskID,
		RemoteContextID:   task.RemoteContextID,
		ReturnImmediately: true,
		Extensions:        append([]string(nil), task.RequestedExtensions...),
		Metadata:          cloneAnyMap(task.ExtensionMetadata),
	}
	callContext := ctx
	var span observability.Span
	if s.Tracer != nil {
		callContext, span = s.Tracer.Start(ctx, "afh.task.continue", map[string]string{
			"afh.tenant_id": tenantID, "afh.task_id": task.ID, "afh.agent_id": agent.ID,
		})
	}
	var continuationErr error
	if span != nil {
		defer func() { span.End(continuationErr) }()
	}
	seen := false
	for observation, observationErr := range s.Adapter.Send(callContext, agent, message) {
		if observationErr != nil {
			continuationErr = observationErr
			return task, observationErr
		}
		seen = true
		task, err = s.applyObservation(ctx, task, observation)
		if err != nil {
			continuationErr = err
			return task, err
		}
	}
	if !seen {
		continuationErr = errors.New("remote Agent returned no continuation result")
		return task, continuationErr
	}
	if span != nil {
		span.End(nil)
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
			previousState := current.State
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
				if s.Metrics != nil && current.State != previousState {
					s.Metrics.IncTaskState(string(current.State))
				}
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
