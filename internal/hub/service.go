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
}

type SubmitTaskInput struct {
	AgentID    string `json:"agentId"`
	Text       string `json:"text"`
	EnablePush bool   `json:"enablePush,omitempty"`
}

type RevokeTokenInput struct {
	Issuer    string    `json:"issuer"`
	TokenID   string    `json:"tokenId"`
	ExpiresAt time.Time `json:"expiresAt"`
	Reason    string    `json:"reason,omitempty"`
}

func (s *Service) RegisterAgent(ctx context.Context, tenantID string, input RegisterAgentInput) (core.Agent, error) {
	if tenantID == "" {
		return core.Agent{}, errors.New("tenant ID is required")
	}
	if err := validateHTTPURL(input.CardURL, !s.AllowPrivateAgentURLs); err != nil {
		return core.Agent{}, fmt.Errorf("card URL: %w", err)
	}
	descriptor, err := s.Adapter.Discover(ctx, input.CardURL)
	if err != nil {
		return core.Agent{}, err
	}
	if err := validateHTTPURL(descriptor.Endpoint, !s.AllowPrivateAgentURLs); err != nil {
		return core.Agent{}, fmt.Errorf("Agent endpoint URL: %w", err)
	}
	declared := make(map[string]struct{}, len(descriptor.SecuritySchemes))
	for _, scheme := range descriptor.SecuritySchemes {
		declared[scheme] = struct{}{}
	}
	credentialEnv := make(map[string]string, len(input.CredentialEnv))
	for scheme, envName := range input.CredentialEnv {
		if _, ok := declared[scheme]; !ok {
			return core.Agent{}, fmt.Errorf("credential scheme %q is not declared by the Agent Card", scheme)
		}
		if s.Secrets == nil {
			return core.Agent{}, errors.New("secret provider is required when credentials are configured")
		}
		if err := s.Secrets.ValidateReference(envName); err != nil {
			return core.Agent{}, fmt.Errorf("credential reference %q: %w", envName, err)
		}
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
		SecuritySchemes:   descriptor.SecuritySchemes, CredentialEnv: credentialEnv,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.PutAgent(ctx, agent); err != nil {
		return core.Agent{}, err
	}
	return agent, nil
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
	agent, err := s.Store.GetAgent(ctx, tenantID, input.AgentID)
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
		if err := validateHTTPURL(s.PublicBaseURL, true); err != nil {
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
	if observation.RemoteTaskID == "" || observation.RemoteTaskID != task.RemoteTaskID {
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
	if observation.RemoteTaskID == "" || observation.RemoteTaskID != task.RemoteTaskID {
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
