package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type TaskReconciler interface {
	ReconcileTask(context.Context, string, string, bool) (core.Task, error)
}

type Reconciler struct {
	Store         core.LeasedStore
	Tasks         TaskReconciler
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	Now           func() time.Time
	Jitter        func(time.Duration) time.Duration
}

func (r *Reconciler) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	for {
		_, err := r.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			// Individual failures are durably rescheduled by RunOnce. The supervisor
			// remains alive so another Task is not blocked behind one provider.
		}
		timer := time.NewTimer(r.pollInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Reconciler) RunOnce(ctx context.Context) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	leases, err := r.Store.ClaimRecoverable(ctx, r.WorkerID, r.batchSize(), r.now(), r.leaseDuration())
	if err != nil {
		return 0, err
	}
	var failures []error
	for _, lease := range leases {
		if err := r.process(ctx, lease); err != nil {
			failures = append(failures, fmt.Errorf("reconcile Task %s: %w", lease.Task.ID, err))
		}
	}
	return len(leases), errors.Join(failures...)
}

func (r *Reconciler) process(ctx context.Context, lease core.WorkLease) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatErrors := make(chan error, 1)
	done := make(chan struct{})
	go r.heartbeat(workCtx, lease, done, heartbeatErrors)
	reconcileResult := make(chan error, 1)
	go func() {
		_, err := r.Tasks.ReconcileTask(workCtx, lease.Task.TenantID, lease.Task.ID, false)
		reconcileResult <- err
	}()
	var reconcileErr error
	select {
	case heartbeatErr := <-heartbeatErrors:
		cancel()
		reconcileErr = <-reconcileResult
		close(done)
		return errors.Join(reconcileErr, heartbeatErr)
	case reconcileErr = <-reconcileResult:
		close(done)
	}
	now := r.now()
	availableAt := now.Add(r.pollInterval())
	resetAttempts := reconcileErr == nil
	if reconcileErr != nil {
		availableAt = now.Add(r.backoff(lease.Attempt))
	}
	if err := r.Store.ReleaseLease(ctx, lease, availableAt, resetAttempts); err != nil {
		return errors.Join(reconcileErr, err)
	}
	return reconcileErr
}

func (r *Reconciler) heartbeat(
	ctx context.Context,
	lease core.WorkLease,
	done <-chan struct{},
	errorsOut chan<- error,
) {
	interval := r.leaseDuration() / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	current := lease
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			renewed, err := r.Store.RenewLease(ctx, current, r.now(), r.leaseDuration())
			if err != nil {
				select {
				case errorsOut <- err:
				default:
				}
				return
			}
			current = renewed
		}
	}
}

func (r *Reconciler) backoff(attempt uint32) time.Duration {
	base := r.BaseBackoff
	if base <= 0 {
		base = time.Second
	}
	maximum := r.MaxBackoff
	if maximum <= 0 {
		maximum = time.Minute
	}
	exponent := min(int(attempt)-1, 30)
	if exponent < 0 {
		exponent = 0
	}
	delay := time.Duration(math.Min(float64(maximum), float64(base)*math.Pow(2, float64(exponent))))
	if r.Jitter != nil {
		delay = r.Jitter(delay)
		if delay < 0 {
			delay = 0
		}
		if delay > maximum {
			delay = maximum
		}
	}
	return delay
}

func (r *Reconciler) validate() error {
	if r.Store == nil || r.Tasks == nil || r.WorkerID == "" {
		return errors.New("reconciler Store, Tasks, and WorkerID are required")
	}
	return nil
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Reconciler) leaseDuration() time.Duration {
	if r.LeaseDuration > 0 {
		return r.LeaseDuration
	}
	return 30 * time.Second
}

func (r *Reconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 5 * time.Second
}

func (r *Reconciler) batchSize() int {
	if r.BatchSize > 0 {
		return r.BatchSize
	}
	return 16
}
