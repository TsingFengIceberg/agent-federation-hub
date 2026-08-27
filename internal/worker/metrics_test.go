package worker

import (
	"strings"
	"testing"
)

func TestOutboxMetricsExposeAggregateCounters(t *testing.T) {
	metrics := &OutboxMetrics{}
	metrics.Claimed.Store(2)
	metrics.Published.Store(1)
	metrics.PublishFailed.Store(1)
	metrics.Retried.Store(1)
	metrics.DeadLettered.Store(1)
	metrics.AckFailed.Store(0)
	output := metrics.Prometheus()
	for _, expected := range []string{
		"afh_outbox_claimed_total 2",
		"afh_outbox_published_total 1",
		"afh_outbox_dead_lettered_total 1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, output)
		}
	}
}
