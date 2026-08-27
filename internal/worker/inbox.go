package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type InboxApplier interface {
	ApplyInboxItem(context.Context, core.InboxItem) (core.Task, error)
}

type InboxProcessor struct {
	Store         core.InboxStore
	Apply         InboxApplier
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	Now           func() time.Time
}

func (p *InboxProcessor) Run(ctx context.Context) error {
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

func (p *InboxProcessor) RunOnce(ctx context.Context) (int, error) {
	if err := p.validate(); err != nil {
		return 0, err
	}
	leases, err := p.Store.ClaimInbox(ctx, p.WorkerID, p.batchSize(), p.now(), p.leaseDuration())
	if err != nil {
		return 0, err
	}
	var failures []error
	for _, lease := range leases {
		if _, err := p.Apply.ApplyInboxItem(ctx, lease.Item); err != nil {
			delay := boundedBackoff(lease.Attempt, p.baseBackoff(), p.maxBackoff())
			if retryErr := p.Store.RetryInbox(ctx, lease, p.now().Add(delay)); retryErr != nil {
				err = errors.Join(err, retryErr)
			}
			failures = append(failures, fmt.Errorf("apply inbox item %s: %w", lease.Item.ID, err))
			continue
		}
		if err := p.Store.AckInbox(ctx, lease); err != nil {
			failures = append(failures, fmt.Errorf("ack inbox item %s: %w", lease.Item.ID, err))
		}
	}
	return len(leases), errors.Join(failures...)
}

func boundedBackoff(attempt uint32, base, maximum time.Duration) time.Duration {
	delay := base
	for index := uint32(1); index < attempt && delay < maximum/2; index++ {
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (p *InboxProcessor) validate() error {
	if p.Store == nil || p.Apply == nil || p.WorkerID == "" {
		return errors.New("inbox processor Store, Apply, and WorkerID are required")
	}
	return nil
}

func (p *InboxProcessor) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *InboxProcessor) batchSize() int {
	if p.BatchSize > 0 {
		return p.BatchSize
	}
	return 32
}

func (p *InboxProcessor) leaseDuration() time.Duration {
	if p.LeaseDuration > 0 {
		return p.LeaseDuration
	}
	return 30 * time.Second
}

func (p *InboxProcessor) pollInterval() time.Duration {
	if p.PollInterval > 0 {
		return p.PollInterval
	}
	return time.Second
}

func (p *InboxProcessor) baseBackoff() time.Duration {
	if p.BaseBackoff > 0 {
		return p.BaseBackoff
	}
	return time.Second
}

func (p *InboxProcessor) maxBackoff() time.Duration {
	if p.MaxBackoff > 0 {
		return p.MaxBackoff
	}
	return time.Minute
}
