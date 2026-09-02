package core

import (
	"context"
	"testing"
	"time"
)

// BenchmarkJournalTaskMutation provides a stable local throughput signal for
// release checks. It intentionally excludes network and model latency.
func BenchmarkJournalTaskMutation(b *testing.B) {
	store, err := OpenJournal("")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for index := 0; index < b.N; index++ {
		taskID := "bench-task-" + NewID()
		now := time.Now().UTC()
		if _, err := store.CreateTask(ctx, Task{ID: taskID, TenantID: "bench", AgentID: "agent", MessageID: NewID(), State: TaskStateSubmitted, Delivery: DeliveryPending, CreatedAt: now, UpdatedAt: now}, Event{Type: "task.created", Source: "bench", CreatedAt: now}); err != nil {
			b.Fatal(err)
		}
	}
}
