package interop

import (
	"context"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func TestMessageScenarioReturnsMessageWithoutTask(t *testing.T) {
	execCtx := &a2asrv.ExecutorContext{
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(ScenarioMessage)),
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
	}

	var events []a2a.Event
	for event, err := range (ScenarioExecutor{}).Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if _, ok := events[0].(*a2a.Message); !ok {
		t.Fatalf("got %T, want *a2a.Message", events[0])
	}
}

func TestTaskScenarioUsesUTCStatusTimestamps(t *testing.T) {
	execCtx := &a2asrv.ExecutorContext{
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("task")),
		TaskID:    a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
	}

	var statuses []*a2a.TaskStatusUpdateEvent
	for event, err := range (ScenarioExecutor{}).Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatal(err)
		}
		if status, ok := event.(*a2a.TaskStatusUpdateEvent); ok {
			statuses = append(statuses, status)
		}
	}

	if len(statuses) != 2 {
		t.Fatalf("got %d status events, want 2", len(statuses))
	}
	for _, status := range statuses {
		if status.Status.Timestamp == nil || status.Status.Timestamp.Location() != time.UTC {
			t.Fatalf("timestamp is not UTC: %v", status.Status.Timestamp)
		}
	}
}

func TestAuthRequiredScenarioCanBeContinuedWithSameTaskContext(t *testing.T) {
	initial := &a2asrv.ExecutorContext{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(ScenarioAuthRequired)),
		TaskID:  a2a.NewTaskID(), ContextID: a2a.NewContextID(),
	}
	var events []a2a.Event
	for event, err := range (ScenarioExecutor{}).Execute(context.Background(), initial) {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("initial events=%d, want submitted plus AUTH_REQUIRED", len(events))
	}
	status, ok := events[1].(*a2a.TaskStatusUpdateEvent)
	if !ok || status.Status.State != a2a.TaskStateAuthRequired {
		t.Fatalf("auth event=%T %+v", events[1], events[1])
	}
	continued := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("authorization-approved")),
		StoredTask: &a2a.Task{ID: initial.TaskID, ContextID: initial.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateAuthRequired}},
		TaskID:     initial.TaskID, ContextID: initial.ContextID,
	}
	var terminal bool
	for event, err := range (ScenarioExecutor{}).Execute(context.Background(), continued) {
		if err != nil {
			t.Fatal(err)
		}
		if value, ok := event.(*a2a.TaskStatusUpdateEvent); ok && value.Status.State == a2a.TaskStateCompleted {
			terminal = true
		}
	}
	if !terminal {
		t.Fatal("AUTH_REQUIRED continuation did not complete")
	}
}
