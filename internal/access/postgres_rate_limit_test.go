package access

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresRateLimiterCoordinatesAcrossPools(t *testing.T) {
	dsn := os.Getenv("AFH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AFH_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	first, err := OpenPostgresRateLimiter(ctx, dsn, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenPostgresRateLimiter(ctx, dsn, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.pool.Exec(ctx, `TRUNCATE afh_rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	first.Now = func() time.Time { return now }
	second.Now = func() time.Time { return now }
	principal := Principal{TenantID: "tenant-a", Subject: "subject-a"}
	request := Request{Action: ActionTaskRead}
	start := make(chan struct{})
	results := make(chan bool, 2)
	var wait sync.WaitGroup
	for _, limiter := range []*PostgresRateLimiter{first, second} {
		wait.Add(1)
		go func(limiter *PostgresRateLimiter) {
			defer wait.Done()
			<-start
			_, allowed := limiter.Allow(ctx, principal, request)
			results <- allowed
		}(limiter)
	}
	close(start)
	wait.Wait()
	close(results)
	allowedCount := 0
	for allowed := range results {
		if allowed {
			allowedCount++
		}
	}
	if allowedCount != 1 {
		t.Fatalf("allowed requests=%d, want exactly one", allowedCount)
	}
	second.Now = func() time.Time { return now.Add(time.Second) }
	if _, allowed := second.Allow(ctx, principal, request); !allowed {
		t.Fatal("token did not refill after one second")
	}
}
