package worker

import (
	"fmt"
	"sync/atomic"
)

// OutboxMetrics contains process-local counters for operational visibility.
// The counters are advisory and reset on process restart; durable delivery
// state remains in the configured OutboxStore.
type OutboxMetrics struct {
	Claimed       atomic.Uint64
	Published     atomic.Uint64
	PublishFailed atomic.Uint64
	Retried       atomic.Uint64
	DeadLettered  atomic.Uint64
	AckFailed     atomic.Uint64
}

func (m *OutboxMetrics) Prometheus() string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf(
		"# HELP afh_outbox_claimed_total Outbox items claimed by workers.\n"+
			"# TYPE afh_outbox_claimed_total counter\n"+
			"afh_outbox_claimed_total %d\n"+
			"# HELP afh_outbox_published_total Outbox items published successfully.\n"+
			"# TYPE afh_outbox_published_total counter\n"+
			"afh_outbox_published_total %d\n"+
			"# HELP afh_outbox_publish_failures_total Outbox publish attempts that failed.\n"+
			"# TYPE afh_outbox_publish_failures_total counter\n"+
			"afh_outbox_publish_failures_total %d\n"+
			"# HELP afh_outbox_retried_total Outbox items scheduled for retry.\n"+
			"# TYPE afh_outbox_retried_total counter\n"+
			"afh_outbox_retried_total %d\n"+
			"# HELP afh_outbox_dead_lettered_total Outbox items moved to dead letter.\n"+
			"# TYPE afh_outbox_dead_lettered_total counter\n"+
			"afh_outbox_dead_lettered_total %d\n"+
			"# HELP afh_outbox_ack_failures_total Outbox acknowledgements that failed.\n"+"# TYPE afh_outbox_ack_failures_total counter\n"+"afh_outbox_ack_failures_total %d\n",
		m.Claimed.Load(), m.Published.Load(), m.PublishFailed.Load(),
		m.Retried.Load(), m.DeadLettered.Load(), m.AckFailed.Load(),
	)
}
