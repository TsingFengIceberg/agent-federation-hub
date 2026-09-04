package orchestration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestPostgresInputStoreRoundTripAndTenantIsolation(t *testing.T) {
	dsn := os.Getenv("AFH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AFH_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	store, err := core.OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.SQLExecutor().Exec(ctx, `TRUNCATE afh_workflow_inputs`); err != nil {
		t.Fatal(err)
	}
	keys := &testWorkflowKeyProvider{key: []byte("01234567890123456789012345678901")}
	inputStore, err := NewPostgresInputStore(store.SQLExecutor(), keys)
	if err != nil {
		t.Fatal(err)
	}
	input := WorkflowInput{Text: "provider-private input", Parts: []core.Part{{Text: "structured context"}}}
	ref, err := inputStore.Put(ctx, "tenant-a", "workflow-a", "step-a", input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "tenant-a/") {
		t.Fatalf("reference=%q", ref)
	}
	var encoded []byte
	err = store.SQLExecutor().QueryRow(ctx, `SELECT payload FROM afh_workflow_inputs WHERE tenant_id=$1 AND reference=$2`, "tenant-a", ref).Scan(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider-private input") || strings.Contains(string(encoded), "structured context") {
		t.Fatal("plaintext Workflow input was persisted in PostgreSQL")
	}
	recovered, err := inputStore.Get(ctx, "tenant-a", ref)
	if err != nil || !workflowInputsEqual(recovered, input) {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	if _, err := inputStore.Get(ctx, "tenant-b", ref); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-tenant read err=%v, want not found", err)
	}
	if _, err := inputStore.Put(ctx, "tenant-a", "workflow-a", "step-a", WorkflowInput{Text: "different"}); err == nil {
		t.Fatal("conflicting Workflow input unexpectedly replaced existing value")
	}
	if keys.generateCalls != 1 {
		t.Fatalf("idempotent Put generated %d data keys, want 1", keys.generateCalls)
	}
}

type testWorkflowKeyProvider struct {
	key           []byte
	generateCalls int
}

func (s *testWorkflowKeyProvider) Current(context.Context) (string, []byte, error) {
	s.generateCalls++
	return "workflow-key-ref", append([]byte(nil), s.key...), nil
}

func (s *testWorkflowKeyProvider) ByID(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.key...), nil
}
