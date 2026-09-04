package main

import (
	"context"
	"errors"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func continuationRequest(taskID string) *a2a.SendMessageRequest {
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))
	message.TaskID = a2a.TaskID(taskID)
	return &a2a.SendMessageRequest{Message: message}
}

func TestRetryContinuationDoesNotReplayNewTask(t *testing.T) {
	calls := 0
	_, err := retryContinuation(context.Background(), continuationRequest(""), func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 5, 0, 0)
	if calls != 1 {
		t.Fatalf("new Task sender called %d times, want 1", calls)
	}
	if err == nil || err.Error() != "task execution is already in progress" {
		t.Fatalf("error=%v, want original admission error", err)
	}
}

func TestRetryContinuationRetriesExistingTask(t *testing.T) {
	calls := 0
	wanted := &a2a.Task{}
	result, err := retryContinuation(context.Background(), continuationRequest("remote-task"), func() (a2a.SendMessageResult, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("task execution is already in progress")
		}
		return wanted, nil
	}, 5, 0, 0)
	if err != nil || result != wanted {
		t.Fatalf("result=%v err=%v, want successful continuation", result, err)
	}
	if calls != 3 {
		t.Fatalf("existing Task sender called %d times, want 3", calls)
	}
}

func TestRetryContinuationStopsAtBoundedAttempts(t *testing.T) {
	calls := 0
	_, err := retryContinuation(context.Background(), continuationRequest("remote-task"), func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 3, 0, 0)
	if calls != 3 {
		t.Fatalf("sender called %d times, want bounded 3", calls)
	}
	if err == nil || err.Error() != "task execution is already in progress" {
		t.Fatalf("error=%v, want final admission error", err)
	}
}

func TestRetryContinuationDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	wanted := errors.New("upstream connection reset")
	_, err := retryContinuation(context.Background(), continuationRequest("remote-task"), func() (a2a.SendMessageResult, error) {
		calls++
		return nil, wanted
	}, 5, 0, 0)
	if calls != 1 {
		t.Fatalf("unrelated error caused %d attempts, want 1", calls)
	}
	if !errors.Is(err, wanted) {
		t.Fatalf("error=%v, want original transport error", err)
	}
}

func TestRetryContinuationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := retryContinuation(ctx, continuationRequest("remote-task"), func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 5, 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("canceled sender called %d times, want 0", calls)
	}
}
