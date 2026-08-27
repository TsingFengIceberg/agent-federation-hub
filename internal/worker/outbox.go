package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// OutboxPublisher applies a committed event to an external sink. Implementations
// must be idempotent because a crash after Publish and before AckOutbox causes a
// deliberate redelivery.
type OutboxPublisher interface {
	Publish(context.Context, core.OutboxItem) error
}

// OutboxProcessor drains the durable outbox with database-backed leases. It is
// safe to run multiple processors against the same PostgreSQL store.
type OutboxProcessor struct {
	Store         core.OutboxStore
	Publisher     OutboxPublisher
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	Now           func() time.Time
}

func (p *OutboxProcessor) Run(ctx context.Context) error {
	if err := p.validate(); err != nil {
		return err
	}
	for {
		_, _ = p.RunOnce(ctx)
		timer := time.NewTimer(p.pollInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *OutboxProcessor) RunOnce(ctx context.Context) (int, error) {
	if err := p.validate(); err != nil {
		return 0, err
	}
	leases, err := p.Store.ClaimOutbox(ctx, p.WorkerID, p.batchSize(), p.now(), p.leaseDuration())
	if err != nil {
		return 0, err
	}
	var failures []error
	for _, lease := range leases {
		if err := p.Publisher.Publish(ctx, lease.Item); err != nil {
			delay := boundedBackoff(lease.Attempt, p.baseBackoff(), p.maxBackoff())
			if retryErr := p.Store.RetryOutbox(ctx, lease, p.now().Add(delay)); retryErr != nil {
				err = errors.Join(err, retryErr)
			}
			failures = append(failures, fmt.Errorf("publish outbox item %s: %w", lease.Item.ID, err))
			continue
		}
		if err := p.Store.AckOutbox(ctx, lease); err != nil {
			failures = append(failures, fmt.Errorf("ack outbox item %s: %w", lease.Item.ID, err))
		}
	}
	return len(leases), errors.Join(failures...)
}

func (p *OutboxProcessor) validate() error {
	if p.Store == nil || p.Publisher == nil || p.WorkerID == "" {
		return errors.New("outbox processor Store, Publisher, and WorkerID are required")
	}
	return nil
}

func (p *OutboxProcessor) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *OutboxProcessor) batchSize() int {
	if p.BatchSize > 0 {
		return p.BatchSize
	}
	return 32
}

func (p *OutboxProcessor) leaseDuration() time.Duration {
	if p.LeaseDuration > 0 {
		return p.LeaseDuration
	}
	return 30 * time.Second
}

func (p *OutboxProcessor) pollInterval() time.Duration {
	if p.PollInterval > 0 {
		return p.PollInterval
	}
	return time.Second
}

func (p *OutboxProcessor) baseBackoff() time.Duration {
	if p.BaseBackoff > 0 {
		return p.BaseBackoff
	}
	return time.Second
}

func (p *OutboxProcessor) maxBackoff() time.Duration {
	if p.MaxBackoff > 0 {
		return p.MaxBackoff
	}
	return time.Minute
}
