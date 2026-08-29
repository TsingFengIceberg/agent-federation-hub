package hub

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

type fakeAdapter struct {
	descriptor     federation.Descriptor
	discover       func(context.Context, string) (federation.Descriptor, error)
	send           func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error]
	get            func(context.Context, core.Agent, string) (federation.Observation, error)
	cancel         func(context.Context, core.Agent, string) (federation.Observation, error)
	subscribe      func(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error]
	sendCalls      int
	getCalls       int
	subscribeCalls int
	lastMessage    federation.Message
}

func (f *fakeAdapter) Discover(ctx context.Context, cardURL string) (federation.Descriptor, error) {
	if f.discover != nil {
		return f.discover(ctx, cardURL)
	}
	descriptor := f.descriptor
	if descriptor.Endpoint == "" {
		descriptor.Endpoint = "https://agent.example/a2a"
	}
	return descriptor, nil
}

func (f *fakeAdapter) Send(ctx context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	f.sendCalls++
	f.lastMessage = message
	if f.send != nil {
		return f.send(ctx, agent, message)
	}
	return emptySequence()
}

func (f *fakeAdapter) Get(ctx context.Context, agent core.Agent, id string) (federation.Observation, error) {
	f.getCalls++
	if f.get != nil {
		return f.get(ctx, agent, id)
	}
	return federation.Observation{}, errors.New("unexpected Get")
}

func (f *fakeAdapter) Cancel(ctx context.Context, agent core.Agent, id string) (federation.Observation, error) {
	if f.cancel != nil {
		return f.cancel(ctx, agent, id)
	}
	return federation.Observation{}, errors.New("unexpected Cancel")
}

func (f *fakeAdapter) Subscribe(ctx context.Context, agent core.Agent, id string) iter.Seq2[federation.Observation, error] {
	f.subscribeCalls++
	if f.subscribe != nil {
		return f.subscribe(ctx, agent, id)
	}
	return emptySequence()
}

func emptySequence() iter.Seq2[federation.Observation, error] {
	return func(func(federation.Observation, error) bool) {}
}

func sequence(values ...any) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		for _, value := range values {
			switch typed := value.(type) {
			case federation.Observation:
				if !yield(typed, nil) {
					return
				}
			case error:
				yield(federation.Observation{}, typed)
				return
			}
		}
	}
}

func newTestService(t *testing.T, store core.Store, adapter *fakeAdapter) *Service {
	t.Helper()
	return &Service{
		Store: store, Adapter: adapter,
		Now:            func() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) },
		TokenGenerator: func() (string, error) { return "push-secret", nil },
	}
}

func registerTestAgent(t *testing.T, service *Service, tenantID string) core.Agent {
	t.Helper()
	agent, err := service.RegisterAgent(context.Background(), tenantID, RegisterAgentInput{
		ID: "agent-1", CardURL: "https://agent.example/card.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func TestRegisterAgentWithPolicyChecksDiscoveredCard(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{descriptor: federation.Descriptor{
		Name: "research", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0",
		Endpoint: "https://agent.example/a2a", Streaming: true,
		Skills: []string{"research"},
	}}
	service := newTestService(t, store, adapter)
	agent, err := service.RegisterAgentWithPolicy(context.Background(), "tenant-a", RegisterAgentInput{
		ID: "agent-policy", CardURL: "https://agent.example/card.json",
	}, AgentRegistrationPolicy{
		RequiredProtocolVersion: "1.0", RequiredProtocolBinding: "JSONRPC",
		RequiredStreamTransport: "SSE", RequireStreaming: true,
		RequiredSkills: []string{"research"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Skills) != 1 || agent.Skills[0] != "research" {
		t.Fatalf("agent skills=%v", agent.Skills)
	}

	if _, err := service.RegisterAgentWithPolicy(context.Background(), "tenant-a", RegisterAgentInput{
		ID: "agent-missing-skill", CardURL: "https://agent.example/card.json",
	}, AgentRegistrationPolicy{RequiredSkills: []string{"finance"}}); err == nil {
		t.Fatal("missing required skill was accepted")
	}
}

func TestResolveAgentBySkillAndRefreshHealth(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{descriptor: federation.Descriptor{
		Name: "research", ProviderVersion: "2", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0",
		Endpoint: "https://agent.example/v2", Skills: []string{"research"}, Streaming: true,
	}}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")

	selected, err := service.ResolveAgent(context.Background(), "tenant-a", "", "research")
	if err != nil || selected.ID != "agent-1" {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{Skill: "research", Text: "work"})
	if err != nil || task.AgentID != "agent-1" {
		t.Fatalf("skill-routed task=%+v err=%v", task, err)
	}

	refreshed, err := service.RefreshAgent(context.Background(), "tenant-a", "agent-1")
	if err != nil || refreshed.HealthStatus != core.AgentHealthHealthy || refreshed.Endpoint != "https://agent.example/v2" {
		t.Fatalf("refreshed=%+v err=%v", refreshed, err)
	}
	adapter.discover = func(context.Context, string) (federation.Descriptor, error) {
		return federation.Descriptor{}, errors.New("provider unavailable")
	}
	unhealthy, err := service.RefreshAgent(context.Background(), "tenant-a", "agent-1")
	if err == nil || unhealthy.HealthStatus != core.AgentHealthUnhealthy {
		t.Fatalf("failed refresh status=%+v err=%v", unhealthy, err)
	}
	if _, err := service.ResolveAgent(context.Background(), "tenant-a", "", "research"); err == nil {
		t.Fatal("unhealthy Agent remained eligible for skill routing")
	}
}

func TestSubmitDisconnectWithKnownRemoteTaskReconcilesWithoutResend(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	disconnect := &federation.Error{Problem: core.Problem{
		Category: "transport", Code: "REMOTE_TRANSPORT_ERROR", Message: "remote Agent request failed",
		Retryable: true, Ambiguous: true,
	}}
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Streaming: true},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(
				federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking},
				disconnect,
			)
		},
		get: func(context.Context, core.Agent, string) (federation.Observation, error) {
			return federation.Observation{DedupKey: "snapshot", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking}, nil
		},
		subscribe: func(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "subscribed-complete", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateCompleted})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != core.TaskStateCompleted || task.Delivery != core.DeliveryAcknowledged {
		t.Fatalf("unexpected reconciled task: %+v", task)
	}
	if adapter.sendCalls != 1 || adapter.getCalls != 1 || adapter.subscribeCalls != 1 {
		t.Fatalf("calls: send=%d get=%d subscribe=%d", adapter.sendCalls, adapter.getCalls, adapter.subscribeCalls)
	}
	if !adapter.lastMessage.ReturnImmediately {
		t.Fatal("Hub did not request immediate A2A acceptance before background reconciliation")
	}
}

func TestSubmitDisconnectBeforeAcknowledgementIsAmbiguousAndNotResent(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(&federation.Error{Problem: core.Problem{
				Category: "transport", Code: "REMOTE_TRANSPORT_ERROR", Message: "remote Agent request failed",
				Retryable: true, Ambiguous: true,
			}})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Delivery != core.DeliveryAmbiguous || task.State != core.TaskStateSubmitted || task.Problem == nil {
		t.Fatalf("unexpected ambiguous task: %+v", task)
	}
	if adapter.sendCalls != 1 || adapter.getCalls != 0 {
		t.Fatalf("calls: send=%d get=%d", adapter.sendCalls, adapter.getCalls)
	}
}

func TestContinueTaskUsesExistingRemoteTaskAndContext(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(_ context.Context, _ core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
			if message.RemoteTaskID == "" {
				return sequence(federation.Observation{
					DedupKey: "input-required", Source: "a2a", RemoteTaskID: "remote-1",
					RemoteContextID: "context-1", State: core.TaskStateInputRequired,
				})
			}
			if message.RemoteTaskID != "remote-1" || message.RemoteContextID != "context-1" || !message.ReturnImmediately {
				return sequence(errors.New("continuation did not preserve remote identifiers"))
			}
			return sequence(federation.Observation{
				DedupKey: "completed", Source: "a2a", RemoteTaskID: "remote-1",
				RemoteContextID: "context-1", State: core.TaskStateCompleted,
			})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	paused, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "needs confirmation"})
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != core.TaskStateInputRequired {
		t.Fatalf("initial state = %s", paused.State)
	}
	completed, err := service.ContinueTask(context.Background(), "tenant-a", paused.ID, ContinueTaskInput{Text: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != core.TaskStateCompleted || completed.RemoteTaskID != "remote-1" || completed.RemoteContextID != "context-1" {
		t.Fatalf("continued task = %+v", completed)
	}
}

func TestRestartRecoveryReconcilesPersistedTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	store, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	adapter.get = func(context.Context, core.Agent, string) (federation.Observation, error) {
		return federation.Observation{DedupKey: "completed", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateCompleted}, nil
	}
	recoveredService := newTestService(t, reopened, adapter)
	if err := recoveredService.Recover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	recovered, err := recoveredService.GetTask(context.Background(), "tenant-a", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != core.TaskStateCompleted {
		t.Fatalf("state = %s", recovered.State)
	}
}

func TestDuplicatePushAndTenantIsolation(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{
			Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", PushNotifications: true,
		},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
	}
	service := newTestService(t, store, adapter)
	service.PublicBaseURL = "https://hub.example"
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work", EnablePush: true})
	if err != nil {
		t.Fatal(err)
	}
	observation := federation.Observation{DedupKey: "push-1", RemoteTaskID: "remote-1", State: core.TaskStateCompleted}
	_, err = service.AcceptPush(context.Background(), "tenant-a", task.ID, "push-secret", observation)
	if err != nil {
		t.Fatal(err)
	}
	updated := processOneInboxItem(t, store, service)
	revision := updated.Revision
	duplicate, err := service.AcceptPush(context.Background(), "tenant-a", task.ID, "push-secret", observation)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Revision != revision {
		t.Fatalf("duplicate changed revision: %d -> %d", revision, duplicate.Revision)
	}
	_, err = service.AcceptPush(context.Background(), "tenant-a", task.ID, "push-secret", federation.Observation{
		DedupKey: "late-working", RemoteTaskID: "remote-1", State: core.TaskStateWorking,
	})
	if err != nil {
		t.Fatal(err)
	}
	late := processOneInboxItem(t, store, service)
	if late.State != core.TaskStateCompleted {
		t.Fatalf("late state regressed terminal task to %s", late.State)
	}
	if _, err := service.AcceptPush(context.Background(), "tenant-b", task.ID, "push-secret", observation); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-tenant Push returned %v", err)
	}
	if _, err := service.AcceptPush(context.Background(), "tenant-a", task.ID, "wrong", observation); !errors.Is(err, ErrInvalidPushCredential) {
		t.Fatalf("bad credential returned %v", err)
	}
}

func TestFirstPushCanEstablishRemoteCorrelationBeforeImmediateResponse(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{
			Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", PushNotifications: true,
		},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			// Simulate a provider that accepts the request but sends its first
			// Push event before the immediate SendMessage response is observed.
			return emptySequence()
		},
	}
	service := newTestService(t, store, adapter)
	service.PublicBaseURL = "https://hub.example"
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{
		AgentID: "agent-1", Text: "work", EnablePush: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.RemoteTaskID != "" {
		t.Fatalf("test precondition remote task ID=%q", task.RemoteTaskID)
	}
	if _, err := service.AcceptPush(context.Background(), "tenant-a", task.ID, "push-secret", federation.Observation{
		DedupKey: "first-push", RemoteTaskID: "remote-first", RemoteContextID: "context-first",
		State: core.TaskStateWorking,
	}); err != nil {
		t.Fatal(err)
	}
	updated := processOneInboxItem(t, store, service)
	if updated.RemoteTaskID != "remote-first" || updated.RemoteContextID != "context-first" || updated.State != core.TaskStateWorking {
		t.Fatalf("first Push did not establish correlation: %+v", updated)
	}
	if _, err := service.AcceptPush(context.Background(), "tenant-a", task.ID, "push-secret", federation.Observation{
		DedupKey: "different-remote", RemoteTaskID: "remote-other", State: core.TaskStateCompleted,
	}); !errors.Is(err, ErrPushTaskMismatch) {
		t.Fatalf("remote task ID change returned %v", err)
	}
}

func processOneInboxItem(t *testing.T, store *core.JournalStore, service *Service) core.Task {
	t.Helper()
	leases, err := store.ClaimInbox(context.Background(), "test-inbox-worker", 1, time.Now().UTC(), time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("claim inbox leases=%+v err=%v", leases, err)
	}
	task, err := service.ApplyInboxItem(context.Background(), leases[0].Item)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AckInbox(context.Background(), leases[0]); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCancellationCannotCrossTenant(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelTask(context.Background(), "tenant-b", task.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-tenant cancel returned %v", err)
	}
	unchanged, _ := service.GetTask(context.Background(), "tenant-a", task.ID)
	if unchanged.CancelRequested {
		t.Fatal("cross-tenant cancellation changed task")
	}
}

func TestArtifactOnlyObservationDoesNotClearTaskState(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.applyObservation(context.Background(), task, federation.Observation{
		DedupKey: "artifact-only", Source: "a2a", RemoteTaskID: "remote-1",
		Artifacts: []federation.ArtifactUpdate{{Artifact: core.Artifact{
			ID: "artifact-1", Complete: true, Parts: []core.Part{{Kind: core.PartText, Text: "result"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != core.TaskStateWorking || len(task.Artifacts) != 1 {
		t.Fatalf("artifact observation changed task incorrectly: %+v", task)
	}
}

func TestRawArtifactIsExternalizedBeforeTaskMutation(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	objects, err := artifactstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{
				DedupKey: "completed-with-file", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateCompleted,
				Artifacts: []federation.ArtifactUpdate{{Artifact: core.Artifact{
					ID: "artifact-1", Complete: true, Parts: []core.Part{{
						Kind: core.PartFile, MediaType: "text/plain", Filename: "result.txt",
						BytesBase64: base64.StdEncoding.EncodeToString([]byte("externalized result")),
					}},
				}}},
			})
		},
	}
	service := newTestService(t, store, adapter)
	service.Artifacts = &artifactstore.Service{
		Metadata: store, Objects: objects, Scanner: artifactstore.NoopScanner{},
		Policy: artifactstore.Policy{
			MaxBytes: 1024, AllowedMIME: map[string]struct{}{"text/plain": {}},
			Quota: artifactstore.Quota{MaxBytes: 1024, MaxObjects: 10}, Retention: time.Hour,
		},
		Now: service.Now,
	}
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	part := task.Artifacts[0].Parts[0]
	if part.ObjectID == "" || part.BytesBase64 != "" || part.URI != "" || part.SHA256 == "" || part.SizeBytes != int64(len("externalized result")) {
		t.Fatalf("Task retained inline Artifact bytes: %+v", part)
	}
	reader, _, err := service.Artifacts.Open(context.Background(), "tenant-a", part.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	payload, _ := io.ReadAll(reader)
	if string(payload) != "externalized result" {
		t.Fatalf("object payload=%q", payload)
	}
	storedEvents, err := store.EventsAfter(context.Background(), "tenant-a", task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range storedEvents {
		if event.Artifact != nil && event.Artifact.Parts[0].BytesBase64 != "" {
			t.Fatal("Event retained inline Artifact bytes")
		}
	}
}

func TestCancelFailureRemainsUnconfirmedAndReturnsError(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	remoteErr := &federation.Error{Problem: core.Problem{
		Category: "transport", Code: "REMOTE_TRANSPORT_ERROR", Message: "remote Agent request failed", Retryable: true,
	}}
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
		cancel: func(context.Context, core.Agent, string) (federation.Observation, error) {
			return federation.Observation{}, remoteErr
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, _ := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	updated, err := service.CancelTask(context.Background(), "tenant-a", task.ID)
	if err == nil || !updated.CancelRequested || updated.State != core.TaskStateWorking || updated.Problem == nil {
		t.Fatalf("unconfirmed cancellation: task=%+v err=%v", updated, err)
	}
}

func TestPushSecretIsNotPersistedInJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	store, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", PushNotifications: true},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking})
		},
	}
	service := newTestService(t, store, adapter)
	service.PublicBaseURL = "https://hub.example"
	registerTestAgent(t, service, "tenant-a")
	if _, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work", EnablePush: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "push-secret") {
		t.Fatal("plaintext Push secret persisted in journal")
	}
}

func TestCredentialEnvironmentReferenceRequiresOperatorAllowlist(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{descriptor: federation.Descriptor{
		Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", SecuritySchemes: []string{"oauth"},
	}}
	service := newTestService(t, store, adapter)
	input := RegisterAgentInput{
		ID: "agent-1", CardURL: "https://agent.example/card.json",
		CredentialEnv: map[string]string{"oauth": "REMOTE_AGENT_TOKEN"},
	}
	if _, err := service.RegisterAgent(context.Background(), "tenant-a", input); err == nil {
		t.Fatal("unapproved credential environment reference was accepted")
	}
	service.Secrets = secrets.NewEnvProviderForTest(map[string]string{"REMOTE_AGENT_TOKEN": "test-secret"})
	agent, err := service.RegisterAgent(context.Background(), "tenant-a", input)
	if err != nil {
		t.Fatal(err)
	}
	if agent.CredentialEnv["oauth"] != "REMOTE_AGENT_TOKEN" {
		t.Fatalf("credential reference=%v", agent.CredentialEnv)
	}
}

func TestOlderRemoteTimestampCannotRegressStateOrObservationCursor(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	newer := time.Date(2026, 8, 27, 12, 1, 0, 0, time.UTC)
	older := newer.Add(-time.Minute)
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "agent", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(federation.Observation{
				DedupKey: "working-newer", Source: "a2a", RemoteTaskID: "remote-1",
				State: core.TaskStateWorking, RemoteObservedAt: &newer,
			})
		},
	}
	service := newTestService(t, store, adapter)
	registerTestAgent(t, service, "tenant-a")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = service.applyObservation(context.Background(), task, federation.Observation{
		DedupKey: "completed-older", Source: "a2a", RemoteTaskID: "remote-1",
		State: core.TaskStateCompleted, RemoteObservedAt: &older,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != core.TaskStateWorking || task.LastRemoteObservedAt == nil || !task.LastRemoteObservedAt.Equal(newer) {
		t.Fatalf("older observation regressed task: %+v", task)
	}
}
