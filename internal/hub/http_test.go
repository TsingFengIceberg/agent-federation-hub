package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	handler := (&HTTPHandler{Service: service}).Handler()

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
	handler := (&HTTPHandler{Service: service}).Handler()

	response := request(t, handler, http.MethodGet, "/v1/agents", "", "", nil)
	if response.Code != http.StatusBadRequest {
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
	handler := (&HTTPHandler{Service: service, DecodePush: decode, MaxBodyBytes: 8}).Handler()
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

func responseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
