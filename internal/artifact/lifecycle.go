package artifact

import (
	"context"
	"errors"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

var ErrWorkerPaused = errors.New("artifact lifecycle worker is paused or draining")

type ClaimGate interface{ AllowClaims() bool }

type Lifecycle struct {
	Metadata      core.ArtifactMetadataStore
	Objects       ObjectStore
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	Now           func() time.Time
	Gate          ClaimGate
}

func (w *Lifecycle) Run(ctx context.Context) error {
	if w.Metadata == nil || w.Objects == nil || w.WorkerID == "" {
		return errors.New("artifact lifecycle metadata, object store, and worker ID are required")
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			// Individual objects are rescheduled below; a batch query failure is
			// retried on the next poll without stopping the process.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Lifecycle) RunOnce(ctx context.Context) error {
	if w.Gate != nil && !w.Gate.AllowClaims() {
		return ErrWorkerPaused
	}
	now := w.now()
	batch := w.BatchSize
	if batch <= 0 {
		batch = 32
	}
	duration := w.LeaseDuration
	if duration <= 0 {
		duration = time.Minute
	}
	leases, err := w.Metadata.ClaimExpiredArtifacts(ctx, w.WorkerID, batch, now, duration)
	if err != nil {
		return err
	}
	var failures []error
	for _, lease := range leases {
		if err := w.Objects.Delete(WithTenantKeyContext(ctx, lease.Object.TenantID), lease.Object.StorageKey); err != nil {
			delay := time.Duration(min(lease.Attempt, 10)) * time.Minute
			if retryErr := w.Metadata.RetryArtifactDeletion(ctx, lease, now.Add(delay)); retryErr != nil {
				failures = append(failures, errors.Join(err, retryErr))
			} else {
				failures = append(failures, err)
			}
			continue
		}
		if err := w.Metadata.CompleteArtifactDeletion(ctx, lease, w.now()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *Lifecycle) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
