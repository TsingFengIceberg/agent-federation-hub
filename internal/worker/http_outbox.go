package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// HTTPOutboxPublisher delivers one outbox envelope to a configured HTTPS
// endpoint. The receiver must treat Idempotency-Key as stable across retries.
type HTTPOutboxPublisher struct {
	Endpoint string
	Client   *http.Client
	Bearer   func(context.Context) (string, error)
}

func NewHTTPOutboxPublisher(endpoint string, bearer func(context.Context) (string, error)) (*HTTPOutboxPublisher, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("outbox endpoint must be an HTTPS URL without user information")
	}
	return &HTTPOutboxPublisher{Endpoint: parsed.String(), Client: &http.Client{}, Bearer: bearer}, nil
}

func (p *HTTPOutboxPublisher) Publish(ctx context.Context, item core.OutboxItem) error {
	if p == nil || p.Endpoint == "" {
		return errors.New("outbox publisher endpoint is required")
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode outbox envelope: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", item.TenantID+":"+item.DedupKey)
	request.Header.Set("X-AFH-Outbox-ID", item.ID)
	request.Header.Set("X-AFH-Tenant-ID", item.TenantID)
	request.Header.Set("X-AFH-Task-ID", item.TaskID)
	request.Header.Set("X-AFH-Topic", item.Topic)
	if p.Bearer != nil {
		token, err := p.Bearer(ctx)
		if err != nil {
			return fmt.Errorf("resolve outbox credential: %w", err)
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("outbox credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("publish outbox item: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("outbox endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
