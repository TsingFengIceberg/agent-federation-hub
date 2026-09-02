package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestGateModesControlClaimAdmission(t *testing.T) {
	gate := NewGate()
	if gate.Mode() != WorkerRunning || !gate.AllowClaims() {
		t.Fatalf("initial gate mode=%s allow=%v", gate.Mode(), gate.AllowClaims())
	}
	gate.Pause()
	if gate.Mode() != WorkerPaused || gate.AllowClaims() {
		t.Fatalf("paused gate mode=%s allow=%v", gate.Mode(), gate.AllowClaims())
	}
	gate.Resume()
	if gate.Mode() != WorkerRunning || !gate.AllowClaims() {
		t.Fatalf("resumed gate mode=%s allow=%v", gate.Mode(), gate.AllowClaims())
	}
	gate.BeginDrain()
	if gate.Mode() != WorkerDraining || gate.AllowClaims() {
		t.Fatalf("draining gate mode=%s allow=%v", gate.Mode(), gate.AllowClaims())
	}
}

func TestDistributedGatePropagatesControlAcrossInstances(t *testing.T) {
	store, err := core.OpenJournal(filepath.Join(t.TempDir(), "hub.journal"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := NewDistributedGate(context.Background(), store, "hub-workers")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDistributedGate(context.Background(), store, "hub-workers")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PauseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.Mode() != WorkerPaused || second.AllowClaims() {
		t.Fatalf("second gate did not observe pause: mode=%s allow=%v", second.Mode(), second.AllowClaims())
	}
	if err := second.ResumeContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.Mode() != WorkerRunning || !first.AllowClaims() {
		t.Fatalf("first gate did not observe resume: mode=%s allow=%v", first.Mode(), first.AllowClaims())
	}
}

type conflictOnceControlStore struct {
	control core.WorkerControl
	sets    int
}

func (s *conflictOnceControlStore) GetWorkerControl(_ context.Context, scope string) (core.WorkerControl, error) {
	if s.control.Scope == "" {
		s.control = core.WorkerControl{Scope: scope, Mode: string(WorkerRunning)}
	}
	return s.control, nil
}

func (s *conflictOnceControlStore) SetWorkerControl(_ context.Context, control core.WorkerControl, expectedRevision uint64) (core.WorkerControl, error) {
	s.sets++
	if s.sets == 1 {
		s.control.Revision++
		return s.control, core.ErrRevisionConflict
	}
	if expectedRevision != s.control.Revision {
		return s.control, errors.New("unexpected revision")
	}
	s.control.Mode = control.Mode
	s.control.Revision++
	return s.control, nil
}

func TestDistributedGateRetriesRevisionConflict(t *testing.T) {
	store := &conflictOnceControlStore{}
	gate, err := NewDistributedGate(context.Background(), store, "hub-workers")
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.PauseContext(context.Background()); err != nil {
		t.Fatalf("pause after conflict: %v", err)
	}
	if store.sets != 2 || gate.Mode() != WorkerPaused || gate.AllowClaims() {
		t.Fatalf("sets=%d mode=%s allow=%v", store.sets, gate.Mode(), gate.AllowClaims())
	}
}
