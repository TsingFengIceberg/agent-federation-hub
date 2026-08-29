package worker

import (
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestNATSPublisherValidatesEndpointAndSubject(t *testing.T) {
	if _, err := NewNATSPublisher("https://nats.example", "events", nil); err == nil {
		t.Fatal("HTTPS endpoint was accepted")
	}
	if _, err := NewNATSPublisher("nats://nats.example", "", nil); err == nil {
		t.Fatal("empty subject was accepted")
	}
	publisher, err := NewNATSPublisher("nats://nats.example", "afh.events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(t.Context(), core.OutboxItem{ID: "outbox-1", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-1"}); err == nil {
		t.Fatal("unreachable NATS endpoint unexpectedly succeeded")
	}
}
