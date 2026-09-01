// Package transport contains shared outbound transport construction and
// bounded retry primitives for federation control-plane dependencies.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TLSOptions describes the operator-owned trust material for an HTTPS
// dependency. A custom CA replaces the system roots only when supplied.
type TLSOptions struct {
	RootCAs            *x509.CertPool
	ClientCertificate  *tls.Certificate
	ServerName         string
	MinVersion         uint16
	InsecureSkipVerify bool
}

// NewHTTPClient constructs a bounded HTTP client. InsecureSkipVerify is
// intentionally rejected by default callers and exists only for explicit
// local test wiring.
func NewHTTPClient(timeout time.Duration, options TLSOptions) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	minVersion := options.MinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	configuration := &tls.Config{
		MinVersion:         minVersion,
		RootCAs:            options.RootCAs,
		ServerName:         options.ServerName,
		InsecureSkipVerify: options.InsecureSkipVerify, // explicit local fixture option
	}
	if options.ClientCertificate != nil {
		configuration.Certificates = []tls.Certificate{*options.ClientCertificate}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:   configuration,
			ForceAttemptHTTP2: true,
			MaxIdleConns:      100,
			IdleConnTimeout:   90 * time.Second,
		},
	}
}

// RetryPolicy controls bounded retries at the transport boundary. Callers
// must allow only operations with safe replay semantics.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RetryRequest   func(*http.Request) bool
	Sleep          func(context.Context, time.Duration) error
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = 100 * time.Millisecond
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 2 * time.Second
	}
	if p.RetryRequest == nil {
		p.RetryRequest = func(request *http.Request) bool {
			return request.Method == http.MethodGet || request.Method == http.MethodHead
		}
	}
	if p.Sleep == nil {
		p.Sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return p
}

// WithRetry wraps an HTTP client with bounded retries for retryable transport
// failures and 429/502/503/504 responses. Request bodies are replayed only
// when net/http supplied GetBody, so ambiguous writes are never retried by
// accident.
func WithRetry(client *http.Client, policy RetryPolicy) *http.Client {
	if client == nil {
		client = NewHTTPClient(10*time.Second, TLSOptions{})
	}
	policy = policy.normalized()
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = retryRoundTripper{base: base, policy: policy}
	return &clone
}

type retryRoundTripper struct {
	base   http.RoundTripper
	policy RetryPolicy
}

func (t retryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.policy.RetryRequest(request) || t.policy.MaxAttempts <= 1 {
		return t.base.RoundTrip(request)
	}
	var lastErr error
	backoff := t.policy.InitialBackoff
	for attempt := 1; attempt <= t.policy.MaxAttempts; attempt++ {
		attemptRequest := request
		if attempt > 1 {
			if request.GetBody == nil && request.Body != nil {
				return nil, lastErr
			}
			var body io.ReadCloser
			if request.GetBody != nil {
				var err error
				body, err = request.GetBody()
				if err != nil {
					return nil, err
				}
			}
			attemptRequest = request.Clone(request.Context())
			attemptRequest.Body = body
		}
		response, err := t.base.RoundTrip(attemptRequest)
		if err == nil && !retryableStatus(response.StatusCode) {
			return response, nil
		}
		if response != nil {
			lastErr = fmt.Errorf("retryable HTTP status %d", response.StatusCode)
			if attempt == t.policy.MaxAttempts || !retryableStatus(response.StatusCode) {
				return response, nil
			}
			response.Body.Close()
		} else {
			lastErr = err
			if attempt == t.policy.MaxAttempts {
				return nil, err
			}
		}
		if delay := retryDelay(response, backoff); delay > 0 {
			if err := t.policy.Sleep(request.Context(), delay); err != nil {
				return nil, err
			}
		}
		backoff *= 2
		if backoff > t.policy.MaxBackoff {
			backoff = t.policy.MaxBackoff
		}
	}
	return nil, lastErr
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(response *http.Response, fallback time.Duration) time.Duration {
	if response != nil {
		if value := strings.TrimSpace(response.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
				return time.Duration(seconds) * time.Second
			}
		}
	}
	return fallback
}

var ErrCircuitOpen = errors.New("outbound dependency circuit is open")

// CircuitBreakerPolicy prevents repeated calls to an unavailable dependency
// after bounded retries have been exhausted.
type CircuitBreakerPolicy struct {
	FailureThreshold int
	OpenFor          time.Duration
	IsFailure        func(*http.Response, error) bool
	Now              func() time.Time
}

// WithCircuitBreaker wraps a client with a small in-process breaker. It is a
// request-volume guard, not a distributed health signal; deployments should
// combine it with metrics and service-level routing.
func WithCircuitBreaker(client *http.Client, policy CircuitBreakerPolicy) *http.Client {
	if client == nil {
		client = NewHTTPClient(10*time.Second, TLSOptions{})
	}
	if policy.FailureThreshold <= 0 {
		policy.FailureThreshold = 3
	}
	if policy.OpenFor <= 0 {
		policy.OpenFor = 15 * time.Second
	}
	if policy.IsFailure == nil {
		policy.IsFailure = func(response *http.Response, err error) bool {
			return err != nil || response == nil || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		}
	}
	if policy.Now == nil {
		policy.Now = time.Now
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &circuitRoundTripper{base: base, policy: policy}
	return &clone
}

type circuitRoundTripper struct {
	base      http.RoundTripper
	policy    CircuitBreakerPolicy
	mu        sync.Mutex
	failed    int
	openUntil time.Time
	halfOpen  bool
}

func (t *circuitRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	now := t.policy.Now()
	t.mu.Lock()
	// Keep the single half-open probe reserved until its result has been
	// classified. The probe clears the expiry below, so checking only
	// openUntil would otherwise let concurrent requests bypass this gate.
	if t.halfOpen {
		t.mu.Unlock()
		return nil, ErrCircuitOpen
	}
	wasHalfOpen := false
	if !t.openUntil.IsZero() {
		if now.Before(t.openUntil) {
			t.mu.Unlock()
			return nil, ErrCircuitOpen
		}
		// Allow one half-open probe to avoid a thundering herd after recovery.
		t.openUntil = time.Time{}
		t.failed = 0
		t.halfOpen = true
		wasHalfOpen = true
	}
	t.mu.Unlock()
	response, err := t.base.RoundTrip(request)
	t.mu.Lock()
	failed := t.policy.IsFailure(response, err)
	if !failed {
		t.failed = 0
		t.halfOpen = false
		t.mu.Unlock()
		return response, err
	}
	t.failed++
	if wasHalfOpen || t.failed >= t.policy.FailureThreshold {
		t.openUntil = t.policy.Now().Add(t.policy.OpenFor)
	}
	t.halfOpen = false
	t.mu.Unlock()
	return response, err
}

// LoadTLSOptions validates and parses operator-provided trust material.
func LoadTLSOptions(caPEM, clientCertPEM, clientKeyPEM []byte, serverName string) (TLSOptions, error) {
	options := TLSOptions{ServerName: strings.TrimSpace(serverName), MinVersion: tls.VersionTLS12}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return TLSOptions{}, errors.New("CA bundle contains no certificates")
		}
		options.RootCAs = pool
	}
	if len(clientCertPEM) == 0 && len(clientKeyPEM) == 0 {
		return options, nil
	}
	if len(clientCertPEM) == 0 || len(clientKeyPEM) == 0 {
		return TLSOptions{}, errors.New("client certificate and key must be configured together")
	}
	certificate, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return TLSOptions{}, fmt.Errorf("parse client certificate: %w", err)
	}
	options.ClientCertificate = &certificate
	return options, nil
}

// ReadBounded is used by control-plane clients to avoid unbounded response
// buffering while preserving a clear error for oversized payloads.
func ReadBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response exceeds configured size limit")
	}
	return body, nil
}
