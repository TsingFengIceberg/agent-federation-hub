package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

func TestHTTPAgentTaskAndResumableEventFlow(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "remote", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Streaming: true},
		send: func(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
			return sequence(
				federation.Observation{DedupKey: "working", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateWorking},
				federation.Observation{DedupKey: "done", Source: "a2a", RemoteTaskID: "remote-1", State: core.TaskStateCompleted},
			)
		},
	}
	service := newTestService(t, store, adapter)
	handler := testHTTPHandler(service, nil, 0)

	response := request(t, handler, http.MethodPost, "/v1/agents", `{
		"id":"agent-1", "cardUrl":"https://agent.example/card.json"
	}`, "tenant-a", nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodPost, "/v1/tasks", `{
		"agentId":"agent-1", "text":"do the work"
	}`, "tenant-a", nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", response.Code, response.Body.String())
	}
	var task core.Task
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.State != core.TaskStateCompleted || task.RemoteTaskID != "remote-1" {
		t.Fatalf("task=%+v", task)
	}
	if strings.Contains(response.Body.String(), "pushTokenHash") {
		t.Fatal("internal Push credential hash leaked through HTTP")
	}

	response = request(t, handler, http.MethodGet, "/v1/tasks/"+task.ID, "", "tenant-b", nil)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "tenant-a") {
		t.Fatalf("cross-tenant response=%d %s", response.Code, response.Body.String())
	}

	headers := map[string]string{"Accept": "text/event-stream", "Last-Event-ID": "1"}
	response = request(t, handler, http.MethodGet, "/v1/tasks/"+task.ID+"/events", "", "tenant-a", headers)
	if response.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "id: 1\n") || !strings.Contains(body, "id: 2\n") || !strings.Contains(body, "id: 3\n") {
		t.Fatalf("unexpected SSE replay:\n%s", body)
	}
}

func TestHTTPRequiresTenantAndRejectsUnknownFields(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	service := newTestService(t, store, &fakeAdapter{})
	handler := testHTTPHandler(service, nil, 0)

	response := request(t, handler, http.MethodGet, "/v1/agents", "", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing tenant status=%d", response.Code)
	}
	response = request(t, handler, http.MethodPost, "/v1/tasks", `{
		"agentId":"agent-1", "text":"work", "secret":"must not be accepted"
	}`, "tenant-a", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPPushSecurityAndSizeLimit(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	adapter := &fakeAdapter{
		descriptor: federation.Descriptor{Name: "remote", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", PushNotifications: true},
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
	decode := func(payload []byte) (federation.Observation, error) {
		return federation.Observation{DedupKey: string(payload), RemoteTaskID: "remote-1", State: core.TaskStateCompleted}, nil
	}
	handler := testHTTPHandler(service, decode, 8)
	path := "/v1/tasks/" + task.ID + "/push?tenant=tenant-a"

	response := request(t, handler, http.MethodPost, path, "done", "", map[string]string{"Authorization": "Bearer wrong"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodPost, path, "payload-too-large", "", map[string]string{"Authorization": "Bearer push-secret"})
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large payload status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodPost, path, "done", "", map[string]string{"Authorization": "Bearer push-secret"})
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid Push status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodGet, "/v1/tasks/"+task.ID, "", "tenant-a", nil)
	if strings.Contains(response.Body.String(), "pushTokenHash") {
		t.Fatal("internal Push credential hash leaked through task response")
	}
}

func TestHTTPFollowStreamReadsOnlyCommittedEvents(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	task, err := store.CreateTask(context.Background(), core.Task{
		ID: "follow-task", TenantID: "tenant-a", AgentID: "agent-1",
		State: core.TaskStateWorking, CreatedAt: now, UpdatedAt: now,
	}, core.Event{Type: "task.status", State: core.TaskStateWorking, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, store, &fakeAdapter{})
	handler := (&HTTPHandler{
		Service: service, Authenticator: DevelopmentAuthenticator{},
		Authorizer: access.DefaultScopeAuthorizer(), EventPollInterval: 5 * time.Millisecond,
	}).Handler()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _, _ = store.ApplyTask(context.Background(), task.TenantID, task.ID, "complete", func(current *core.Task) (core.Event, error) {
			current.State = core.TaskStateCompleted
			current.UpdatedAt = time.Now().UTC()
			return core.Event{Type: "task.status", State: current.State, CreatedAt: current.UpdatedAt}, nil
		})
	}()
	request := httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.ID+"/events?after=1&follow=true", nil)
	request.Header.Set(TenantHeader, task.TenantID)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follow stream did not finish after terminal Event")
	}
	body := response.Body.String()
	if !strings.Contains(body, "id: 2\n") || !strings.Contains(body, `"state":"COMPLETED"`) {
		t.Fatalf("follow stream did not deliver committed completion: %s", body)
	}
}

func TestHTTPArtifactContentIsTenantScoped(t *testing.T) {
	store, _ := core.OpenJournal("")
	defer store.Close()
	objects, err := artifactstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	artifacts := &artifactstore.Service{
		Metadata: store, Objects: objects, Scanner: artifactstore.NoopScanner{},
		Policy: artifactstore.Policy{
			MaxBytes: 1024, AllowedMIME: map[string]struct{}{"text/plain": {}},
			Quota: artifactstore.Quota{MaxBytes: 1024, MaxObjects: 10}, Retention: time.Hour,
		},
		Now: func() time.Time { return now },
	}
	object, err := artifacts.Ingest(context.Background(), artifactstore.Input{
		TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "artifact-a",
		DedupKey: "http", PartIndex: 0, MediaType: "text/plain", Filename: "result.txt",
	}, strings.NewReader("downloadable result"))
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, store, &fakeAdapter{})
	service.Artifacts = artifacts
	handler := testHTTPHandler(service, nil, 0)
	response := request(t, handler, http.MethodGet, "/v1/artifacts/"+object.ID+"/content", "", "tenant-a", nil)
	if response.Code != http.StatusOK || response.Body.String() != "downloadable result" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("content status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	response = request(t, handler, http.MethodGet, "/v1/artifacts/"+object.ID, "", "tenant-b", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant Artifact status=%d body=%s", response.Code, response.Body.String())
	}
}

func testHTTPHandler(service *Service, decode PushDecoder, maxBodyBytes int64) http.Handler {
	return (&HTTPHandler{
		Service: service, DecodePush: decode, MaxBodyBytes: maxBodyBytes,
		Authenticator: DevelopmentAuthenticator{}, Authorizer: access.DefaultScopeAuthorizer(),
	}).Handler()
}

func request(t *testing.T, handler http.Handler, method, path, body, tenantID string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if tenantID != "" {
		request.Header.Set(TenantHeader, tenantID)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
