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

// CloudEvent is the structured JSON representation of a Hub Task Event.
// The event data retains the original OutboxItem so consumers can apply their
// own schema validation while using the stable idempotency key.
type CloudEvent struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	Type        string          `json:"type"`
	Subject     string          `json:"subject"`
	Time        string          `json:"time"`
	DataContent string          `json:"datacontenttype"`
	Data        json.RawMessage `json:"data"`
}

// CloudEventsPublisher delivers Outbox records using CloudEvents 1.0 HTTP
// structured content mode. It is intentionally transport-only: durable claim,
// retry, dead-letter and acknowledgement remain owned by OutboxProcessor.
type CloudEventsPublisher struct {
	Endpoint string
	Source   string
	Client   *http.Client
	Bearer   func(context.Context) (string, error)
}

func NewCloudEventsPublisher(endpoint, source string, bearer func(context.Context) (string, error)) (*CloudEventsPublisher, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("CloudEvents endpoint must be an HTTPS URL without user information")
	}
	if strings.TrimSpace(source) == "" {
		source = "urn:agent-federation-hub"
	}
	return &CloudEventsPublisher{Endpoint: parsed.String(), Source: source, Client: &http.Client{}, Bearer: bearer}, nil
}

func (p *CloudEventsPublisher) Publish(ctx context.Context, item core.OutboxItem) error {
	if p == nil || p.Endpoint == "" {
		return errors.New("CloudEvents publisher endpoint is required")
	}
	event := CloudEvent{
		SpecVersion: "1.0",
		ID:          item.ID,
		Source:      p.Source,
		Type:        item.Topic,
		Subject:     item.TaskID,
		Time:        item.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		DataContent: "application/json",
		Data:        item.Payload,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode CloudEvent: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/cloudevents+json; charset=utf-8")
	request.Header.Set("Idempotency-Key", item.TenantID+":"+item.DedupKey)
	request.Header.Set("Ce-Specversion", "1.0")
	request.Header.Set("Ce-Id", item.ID)
	request.Header.Set("Ce-Source", p.Source)
	request.Header.Set("Ce-Type", item.Topic)
	request.Header.Set("Ce-Subject", item.TaskID)
	request.Header.Set("X-AFH-Tenant-ID", item.TenantID)
	if p.Bearer != nil {
		token, err := p.Bearer(ctx)
		if err != nil {
			return fmt.Errorf("resolve CloudEvents credential: %w", err)
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("CloudEvents credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("publish CloudEvent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("CloudEvents endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
