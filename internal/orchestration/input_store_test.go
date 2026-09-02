package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestMemoryInputStoreScopesTenantAndRejectsConflictingWrites(t *testing.T) {
	store := NewMemoryInputStore()
	ref, err := store.Put(context.Background(), "tenant-a", "workflow-a", "step-a", "private prompt")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(context.Background(), "tenant-a", ref); err != nil || got != "private prompt" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := store.Get(context.Background(), "tenant-b", ref); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross tenant err=%v", err)
	}
	if _, err := store.Put(context.Background(), "tenant-a", "workflow-a", "step-a", "different"); err == nil {
		t.Fatal("conflicting input overwrite accepted")
	}
}
