package worker

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

var ErrWorkerPaused = errors.New("worker is paused or draining")

type WorkerMode string

const (
	WorkerRunning  WorkerMode = "RUNNING"
	WorkerPaused   WorkerMode = "PAUSED"
	WorkerDraining WorkerMode = "DRAINING"
)

// Gate coordinates claim admission across all background workers in one Hub
// process. It never cancels in-flight work; leases and context cancellation
// retain ownership and recovery semantics.
type Gate struct{ mode atomic.Int32 }

type ClaimGate interface{ AllowClaims() bool }

func NewGate() *Gate {
	g := &Gate{}
	g.mode.Store(int32(1))
	return g
}

func (g *Gate) Mode() WorkerMode {
	if g == nil {
		return WorkerRunning
	}
	switch g.mode.Load() {
	case 2:
		return WorkerPaused
	case 3:
		return WorkerDraining
	default:
		return WorkerRunning
	}
}

func (g *Gate) ModeString() string { return string(g.Mode()) }

func (g *Gate) Pause() {
	if g != nil {
		g.mode.Store(2)
	}
}
func (g *Gate) Resume() {
	if g != nil {
		g.mode.Store(1)
	}
}
func (g *Gate) BeginDrain() {
	if g != nil {
		g.mode.Store(3)
	}
}
func (g *Gate) AllowClaims() bool { return g == nil || g.mode.Load() == 1 }

// DistributedGate mirrors operator mode in a shared Store while retaining a
// local atomic decision for worker hot paths. Refresh should be called by each
// instance periodically; context-aware setters are used by management routes.
type DistributedGate struct {
	local *Gate
	store core.WorkerControlStore
	scope string
}

func NewDistributedGate(ctx context.Context, store core.WorkerControlStore, scope string) (*DistributedGate, error) {
	if store == nil || scope == "" {
		return nil, errors.New("distributed worker gate store and scope are required")
	}
	gate := &DistributedGate{local: NewGate(), store: store, scope: scope}
	if err := gate.Refresh(ctx); err != nil {
		return nil, err
	}
	return gate, nil
}

func (g *DistributedGate) Mode() WorkerMode {
	if g == nil || g.local == nil {
		return WorkerRunning
	}
	return g.local.Mode()
}

func (g *DistributedGate) ModeString() string { return string(g.Mode()) }
func (g *DistributedGate) AllowClaims() bool  { return g == nil || g.local.AllowClaims() }

func (g *DistributedGate) Refresh(ctx context.Context) error {
	if g == nil || g.store == nil {
		return errors.New("distributed worker gate is not configured")
	}
	control, err := g.store.GetWorkerControl(ctx, g.scope)
	if err != nil {
		return err
	}
	return g.apply(control.Mode)
}

func (g *DistributedGate) set(ctx context.Context, mode WorkerMode) error {
	if g == nil || g.store == nil {
		return errors.New("distributed worker gate is not configured")
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		current, err := g.store.GetWorkerControl(ctx, g.scope)
		if err != nil {
			return err
		}
		control, err := g.store.SetWorkerControl(ctx, core.WorkerControl{Scope: g.scope, Mode: string(mode)}, current.Revision)
		if err == nil {
			return g.apply(control.Mode)
		}
		if !errors.Is(err, core.ErrRevisionConflict) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (g *DistributedGate) apply(mode string) error {
	switch WorkerMode(mode) {
	case WorkerRunning:
		g.local.Resume()
	case WorkerPaused:
		g.local.Pause()
	case WorkerDraining:
		g.local.BeginDrain()
	default:
		return errors.New("unknown distributed worker mode")
	}
	return nil
}

func (g *DistributedGate) PauseContext(ctx context.Context) error  { return g.set(ctx, WorkerPaused) }
func (g *DistributedGate) ResumeContext(ctx context.Context) error { return g.set(ctx, WorkerRunning) }
func (g *DistributedGate) DrainContext(ctx context.Context) error  { return g.set(ctx, WorkerDraining) }
func (g *DistributedGate) Pause()                                  { _ = g.PauseContext(context.Background()) }
func (g *DistributedGate) Resume()                                 { _ = g.ResumeContext(context.Background()) }
func (g *DistributedGate) BeginDrain()                             { _ = g.DrainContext(context.Background()) }

// BeginLocalDrain is used during process shutdown so a transient termination
// does not leave every other Hub instance durably paused.
func (g *DistributedGate) BeginLocalDrain() {
	if g != nil && g.local != nil {
		g.local.BeginDrain()
	}
}
