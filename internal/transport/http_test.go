package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestRetryTransportRetriesSafeGETAndClosesTransientResponses(t *testing.T) {
	var attempts int
	var closed int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: closeRecorder{Reader: strings.NewReader("retry"), closed: &closed}, Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	client = WithRetry(client, RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }})
	response, err := client.Get("https://registry.example/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if attempts != 3 || closed != 2 {
		t.Fatalf("attempts=%d closed=%d", attempts, closed)
	}
}

func TestRetryTransportDoesNotReplayUnsafePOST(t *testing.T) {
	var attempts int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("down")), Request: request}, nil
	})}
	client = WithRetry(client, RetryPolicy{MaxAttempts: 3, Sleep: func(context.Context, time.Duration) error { return nil }})
	request, err := http.NewRequest(http.MethodPost, "https://gateway.example/v1/proxy/send", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if attempts != 1 {
		t.Fatalf("unsafe POST attempts=%d", attempts)
	}
}

func TestLoadTLSOptionsRequiresCertificatePair(t *testing.T) {
	if _, err := LoadTLSOptions(nil, []byte("cert"), nil, ""); err == nil {
		t.Fatal("incomplete client certificate pair accepted")
	}
	if options, err := LoadTLSOptions(nil, nil, nil, "registry.example"); err != nil || options.ServerName != "registry.example" || options.MinVersion != tls.VersionTLS12 {
		t.Fatalf("options=%+v err=%v", options, err)
	}
}

func TestCircuitBreakerOpensAndAllowsHalfOpenProbe(t *testing.T) {
	var now = time.Unix(100, 0)
	var attempts int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("dependency down")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	client = WithCircuitBreaker(client, CircuitBreakerPolicy{FailureThreshold: 2, OpenFor: time.Second, Now: func() time.Time { return now }})
	for range 2 {
		if _, err := client.Get("https://registry.example/healthz"); err == nil {
			t.Fatal("failed dependency unexpectedly succeeded")
		}
	}
	if _, err := client.Get("https://registry.example/healthz"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("circuit error=%v", err)
	}
	now = now.Add(2 * time.Second)
	response, err := client.Get("https://registry.example/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if attempts != 3 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestCircuitBreakerAllowsOnlyOneConcurrentHalfOpenProbe(t *testing.T) {
	now := time.Unix(100, 0)
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	var mu sync.Mutex
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		attempts++
		attempt := attempts
		mu.Unlock()
		if attempt <= 2 {
			return nil, errors.New("dependency down")
		}
		close(probeStarted)
		<-releaseProbe
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	client = WithCircuitBreaker(client, CircuitBreakerPolicy{FailureThreshold: 2, OpenFor: time.Second, Now: func() time.Time { return now }})
	for range 2 {
		if _, err := client.Get("https://registry.example/healthz"); err == nil {
			t.Fatal("failed dependency unexpectedly succeeded")
		}
	}
	now = now.Add(2 * time.Second)
	firstDone := make(chan error, 1)
	go func() {
		response, err := client.Get("https://registry.example/healthz")
		if response != nil {
			response.Body.Close()
		}
		firstDone <- err
	}()
	<-probeStarted
	if _, err := client.Get("https://registry.example/healthz"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("concurrent half-open request error=%v", err)
	}
	close(releaseProbe)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts=%d, want one half-open probe", attempts)
	}
}

func TestCircuitBreakerCanServeRequestsAfterSuccessfulProbe(t *testing.T) {
	now := time.Unix(100, 0)
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("dependency down")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	client = WithCircuitBreaker(client, CircuitBreakerPolicy{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }})
	if _, err := client.Get("https://registry.example/healthz"); err == nil {
		t.Fatal("failed dependency unexpectedly succeeded")
	}
	now = now.Add(2 * time.Second)
	response, err := client.Get("https://registry.example/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = client.Get("https://registry.example/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if attempts != 3 {
		t.Fatalf("attempts=%d, want probe plus follow-up request", attempts)
	}
}

type closeRecorder struct {
	io.Reader
	closed *int
}

func (r closeRecorder) Close() error {
	if r.closed == nil {
		return errors.New("closed counter is nil")
	}
	(*r.closed)++
	return nil
}
