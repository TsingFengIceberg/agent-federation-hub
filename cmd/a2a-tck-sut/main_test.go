package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func TestNormalizePushTaskIDAlias(t *testing.T) {
	for _, method := range []string{
		"CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig",
	} {
		body := []byte(`{"jsonrpc":"2.0","id":7,"method":"` + method + `","params":{"task_id":"task-1","id":"push-1"}}`)
		normalized := normalizePushTaskIDAlias(body)
		var request struct {
			ID     int                        `json:"id"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(normalized, &request); err != nil {
			t.Fatalf("%s normalized body is invalid: %v", method, err)
		}
		var taskID string
		if err := json.Unmarshal(request.Params["taskId"], &taskID); err != nil || taskID != "task-1" {
			t.Fatalf("%s taskId=%q err=%v", method, taskID, err)
		}
		if _, exists := request.Params["task_id"]; exists {
			t.Fatalf("%s retained snake_case task_id", method)
		}
		if request.ID != 7 {
			t.Fatalf("request ID=%d, want 7", request.ID)
		}
	}
}

func TestNormalizePushTaskIDAliasLeavesOtherMethodsUntouched(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"SendMessage","params":{"task_id":"task-1"}}`)
	normalized := normalizePushTaskIDAlias(body)
	if string(normalized) != string(body) {
		t.Fatalf("non-Push request changed: %s", normalized)
	}
}

func TestTCKExecutorAuthRequiredContinuation(t *testing.T) {
	state := newSUTTaskState()
	executor := tckExecutor{state: state}
	initialMessage := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("auth-required"))
	initialMessage.ID = "tck-auth-required-1"
	initialMessage.TaskID = "task-auth-1"
	initialMessage.ContextID = "context-auth-1"
	initial := &a2asrv.ExecutorContext{
		Message: initialMessage, TaskID: initialMessage.TaskID, ContextID: initialMessage.ContextID,
	}
	var states []a2a.TaskState
	for event, err := range executor.Execute(context.Background(), initial) {
		if err != nil {
			t.Fatal(err)
		}
		if task, ok := event.(*a2a.TaskStatusUpdateEvent); ok {
			states = append(states, task.Status.State)
		}
	}
	if len(states) != 1 || states[0] != a2a.TaskStateAuthRequired {
		t.Fatalf("initial states=%v, want AUTH_REQUIRED", states)
	}

	continuation := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("approved"))
	continuation.ID = "tck-auth-required-continue"
	continuation.TaskID = initialMessage.TaskID
	continuation.ContextID = initialMessage.ContextID
	stored := &a2a.Task{ID: initialMessage.TaskID, ContextID: initialMessage.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateAuthRequired}}
	continuationContext := &a2asrv.ExecutorContext{
		Message: continuation, TaskID: continuation.TaskID, ContextID: continuation.ContextID, StoredTask: stored,
	}
	states = states[:0]
	for event, err := range executor.Execute(context.Background(), continuationContext) {
		if err != nil {
			t.Fatal(err)
		}
		if task, ok := event.(*a2a.TaskStatusUpdateEvent); ok {
			states = append(states, task.Status.State)
		}
	}
	if len(states) != 1 || states[0] != a2a.TaskStateCompleted {
		t.Fatalf("continuation states=%v, want COMPLETED", states)
	}
}
