package agentconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRuntimeKeepsLastGoodSnapshotWhenCandidateIsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent_config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var applies atomic.Int32
	runtime, err := NewRuntime(context.Background(), path, func(context.Context, File, bool) error {
		applies.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if applies.Load() != 1 {
		t.Fatalf("initial applies=%d", applies.Load())
	}
	if err := os.WriteFile(path, []byte("schema_version: 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := runtime.Reload(context.Background()); err == nil || changed {
		t.Fatalf("invalid reload changed=%v err=%v", changed, err)
	}
	current, ok := runtime.Current()
	if !ok || current.SchemaVersion != CurrentSchemaVersion || len(current.Agents) != 1 {
		t.Fatalf("current=%+v ok=%v", current, ok)
	}
	if applies.Load() != 1 {
		t.Fatalf("invalid candidate was applied %d times", applies.Load())
	}
}

func TestRuntimeKeepsLastGoodSnapshotWhenApplyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent_config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var reject atomic.Bool
	runtime, err := NewRuntime(context.Background(), path, func(_ context.Context, candidate File, _ bool) error {
		if reject.Load() && len(candidate.Agents) > 0 {
			return errors.New("provider admission failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reject.Store(true)
	updated := strings.Replace(validYAML, "agent-a", "agent-b", 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := runtime.Reload(context.Background()); err == nil || changed {
		t.Fatalf("failed apply changed=%v err=%v", changed, err)
	}
	current, ok := runtime.Current()
	if !ok || current.Agents[0].ID != "agent-a" {
		t.Fatalf("old snapshot was not retained: %+v", current)
	}
}

func TestRuntimePublishesDefensiveCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent_config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := runtime.Current()
	if !ok {
		t.Fatal("missing snapshot")
	}
	first.Agents[0].ID = "mutated"
	second, _ := runtime.Current()
	if second.Agents[0].ID != "agent-a" {
		t.Fatalf("snapshot was mutated through returned copy: %+v", second)
	}
}
