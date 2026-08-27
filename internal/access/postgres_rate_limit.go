package access

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRateLimiter stores token buckets in PostgreSQL so independent Hub
// instances consume the same tenant/subject/action budget.
type PostgresRateLimiter struct {
	pool          *pgxpool.Pool
	ratePerSecond float64
	burst         float64
	Now           func() time.Time
}

// OpenPostgresRateLimiter opens a small independent pool. Keeping the pool
// behind the RateLimiter interface allows a deployment to replace it with a
// gateway or another shared counter without changing HTTP handlers.
func OpenPostgresRateLimiter(ctx context.Context, dataSourceName string, perMinute, burst int) (*PostgresRateLimiter, error) {
	if dataSourceName == "" {
		return nil, errors.New("PostgreSQL rate limiter DSN is required")
	}
	if perMinute <= 0 {
		return nil, errors.New("rate limit per minute must be positive")
	}
	if burst <= 0 {
		burst = perMinute
		if burst > 60 {
			burst = 60
		}
	}
	config, err := pgxpool.ParseConfig(dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL rate limiter configuration: %w", err)
	}
	if config.MaxConns == 0 || config.MaxConns > 4 {
		config.MaxConns = 4
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL rate limiter pool: %w", err)
	}
	limiter := &PostgresRateLimiter{
		pool: pool, ratePerSecond: float64(perMinute) / 60, burst: float64(burst), Now: time.Now,
	}
	if err := limiter.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return limiter, nil
}

func (l *PostgresRateLimiter) migrate(ctx context.Context) error {
	_, err := l.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS afh_rate_limit_buckets (
			tenant_id TEXT NOT NULL,
			subject TEXT NOT NULL,
			action TEXT NOT NULL,
			tokens DOUBLE PRECISION NOT NULL,
			last_refill TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (tenant_id, subject, action)
		)`)
	if err != nil {
		return fmt.Errorf("migrate PostgreSQL rate limiter: %w", err)
	}
	return nil
}

func (l *PostgresRateLimiter) Allow(ctx context.Context, principal Principal, request Request) (time.Duration, bool) {
	if l == nil || l.pool == nil {
		return 0, true
	}
	now := l.now()
	for attempt := 0; attempt < 3; attempt++ {
		retryAfter, allowed, err := l.allowOnce(ctx, principal, request, now)
		if err == nil {
			return retryAfter, allowed
		}
		if !isSerializationFailure(err) {
			// A shared limiter must fail closed when its coordination store is
			// unavailable; callers receive a conservative one-minute retry.
			return time.Minute, false
		}
	}
	return time.Minute, false
}

func (l *PostgresRateLimiter) allowOnce(ctx context.Context, principal Principal, request Request, now time.Time) (time.Duration, bool, error) {
	tx, err := l.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO afh_rate_limit_buckets
		  (tenant_id, subject, action, tokens, last_refill, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (tenant_id, subject, action) DO NOTHING`,
		principal.TenantID, principal.Subject, request.Action, l.burst, now)
	if err != nil {
		return 0, false, err
	}
	var tokens float64
	var lastRefill time.Time
	if err := tx.QueryRow(ctx, `
		SELECT tokens, last_refill FROM afh_rate_limit_buckets
		WHERE tenant_id=$1 AND subject=$2 AND action=$3 FOR UPDATE`,
		principal.TenantID, principal.Subject, request.Action).Scan(&tokens, &lastRefill); err != nil {
		return 0, false, err
	}
	if elapsed := now.Sub(lastRefill).Seconds(); elapsed > 0 {
		tokens = math.Min(l.burst, tokens+elapsed*l.ratePerSecond)
	}
	allowed := tokens >= 1
	if allowed {
		tokens--
	}
	if _, err := tx.Exec(ctx, `
		UPDATE afh_rate_limit_buckets SET tokens=$1, last_refill=$2, updated_at=$2
		WHERE tenant_id=$3 AND subject=$4 AND action=$5`,
		tokens, now, principal.TenantID, principal.Subject, request.Action); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	if allowed {
		return 0, true, nil
	}
	seconds := math.Ceil((1 - tokens) / l.ratePerSecond)
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second, false, nil
}

func (l *PostgresRateLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func (l *PostgresRateLimiter) Close() {
	if l != nil && l.pool != nil {
		l.pool.Close()
	}
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "40001"
}
