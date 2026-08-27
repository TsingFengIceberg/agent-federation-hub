package access

import (
	"context"
	"math"
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(context.Context, Principal, Request) (time.Duration, bool)
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

// TokenBucketLimiter is intentionally process-local. Distributed deployments
// should provide the same interface with a shared counter or gateway limiter.
type TokenBucketLimiter struct {
	mu             sync.Mutex
	ratePerSecond  float64
	burst          float64
	Now            func() time.Time
	buckets        map[string]rateBucket
	pruneAfter     time.Duration
	maxBucketCount int
}

func NewTokenBucketLimiter(perMinute, burst int) *TokenBucketLimiter {
	if perMinute <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = perMinute
		if burst > 60 {
			burst = 60
		}
	}
	return &TokenBucketLimiter{
		ratePerSecond:  float64(perMinute) / 60,
		burst:          float64(burst),
		Now:            time.Now,
		buckets:        make(map[string]rateBucket),
		pruneAfter:     time.Hour,
		maxBucketCount: 100000,
	}
}

func (l *TokenBucketLimiter) Allow(_ context.Context, principal Principal, request Request) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	now := l.nowTime()
	key := principal.TenantID + "\x00" + principal.Subject + "\x00" + string(request.Action)
	l.mu.Lock()
	defer l.mu.Unlock()
	state, exists := l.buckets[key]
	if !exists {
		state = rateBucket{tokens: l.burst, last: now}
	}
	if elapsed := now.Sub(state.last).Seconds(); elapsed > 0 {
		state.tokens = math.Min(l.burst, state.tokens+elapsed*l.ratePerSecond)
		state.last = now
	}
	if state.tokens >= 1 {
		state.tokens--
		l.buckets[key] = state
		if len(l.buckets) > l.maxBucketCount {
			l.prune(now)
		}
		return 0, true
	}
	l.buckets[key] = state
	if l.ratePerSecond <= 0 {
		return time.Hour, false
	}
	retry := time.Duration(math.Ceil((1-state.tokens)/l.ratePerSecond)) * time.Second
	if retry < time.Second {
		retry = time.Second
	}
	return retry, false
}

func (l *TokenBucketLimiter) nowTime() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func (l *TokenBucketLimiter) prune(now time.Time) {
	cutoff := now.Add(-l.pruneAfter)
	for key, state := range l.buckets {
		if state.last.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}
