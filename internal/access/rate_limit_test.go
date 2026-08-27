package access

import (
	"context"
	"testing"
	"time"
)

func TestTokenBucketLimiterScopesByTenantSubjectAndAction(t *testing.T) {
	limiter := NewTokenBucketLimiter(60, 1)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	limiter.Now = func() time.Time { return now }
	principal := Principal{TenantID: "tenant-a", Subject: "subject-a"}
	request := Request{Action: ActionTaskRead, ResourceID: "task-a"}
	if retry, allowed := limiter.Allow(context.Background(), principal, request); !allowed || retry != 0 {
		t.Fatalf("first allow=%v retry=%v", allowed, retry)
	}
	if retry, allowed := limiter.Allow(context.Background(), principal, request); allowed || retry < time.Second {
		t.Fatalf("burst allow=%v retry=%v", allowed, retry)
	}
	limiter.Now = func() time.Time { return now.Add(time.Second) }
	if retry, allowed := limiter.Allow(context.Background(), principal, request); !allowed || retry != 0 {
		t.Fatalf("refill allow=%v retry=%v", allowed, retry)
	}
	other := principal
	other.Subject = "subject-b"
	if retry, allowed := limiter.Allow(context.Background(), other, request); !allowed || retry != 0 {
		t.Fatalf("other subject allow=%v retry=%v", allowed, retry)
	}
}
