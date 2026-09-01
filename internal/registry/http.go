// Package registry contains replaceable Agent Registry adapters. The Hub's
// local Store remains the authoritative runtime cache; an external registry is
// an integration boundary and must expose tenant-scoped, idempotent writes.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/transport"
)

type Client interface {
	Register(context.Context, core.Agent) error
	List(context.Context, string) ([]core.Agent, error)
	Health(context.Context) error
}

type HTTPClient struct {
	Endpoint         string
	Client           *http.Client
	Bearer           func(context.Context) (string, error)
	MaxResponseBytes int64
}

// SetHTTPClient allows an operator to install a transport with a private CA,
// mTLS configuration, or deployment-specific connection pooling. The default
// constructor remains safe for ordinary public HTTPS endpoints.
func (c *HTTPClient) SetHTTPClient(client *http.Client) {
	if c != nil && client != nil {
		c.Client = client
	}
}

// SetRetryPolicy installs bounded retries for safe Registry operations. The
// default policy retries only GET/HEAD requests; registration writes remain
// single-attempt because an arbitrary Registry may not implement idempotency.
func (c *HTTPClient) SetRetryPolicy(policy transport.RetryPolicy) {
	if c != nil {
		c.Client = transport.WithRetry(c.client(), policy)
	}
}

func NewHTTPClient(endpoint string, bearer func(context.Context) (string, error)) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("registry endpoint must be an HTTPS URL without user information")
	}
	return &HTTPClient{Endpoint: strings.TrimRight(parsed.String(), "/"), Client: &http.Client{Timeout: 10 * time.Second}, Bearer: bearer, MaxResponseBytes: 1 << 20}, nil
}

func (c *HTTPClient) Register(ctx context.Context, agent core.Agent) error {
	if strings.TrimSpace(agent.ID) == "" || strings.TrimSpace(agent.TenantID) == "" {
		return errors.New("registry Agent ID and tenant are required")
	}
	return c.doJSON(ctx, http.MethodPost, "/v1/agents", agent, nil)
}

func (c *HTTPClient) List(ctx context.Context, tenant string) ([]core.Agent, error) {
	if strings.TrimSpace(tenant) == "" {
		return nil, errors.New("registry tenant is required")
	}
	request, err := c.request(ctx, http.MethodGet, "/v1/agents?tenant_id="+url.QueryEscape(tenant), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("list registry Agents: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("registry returned HTTP %d", response.StatusCode)
	}
	var agents []core.Agent
	if err := decodeBody(response, &agents, c.MaxResponseBytes); err != nil {
		return nil, err
	}
	for index, agent := range agents {
		if agent.TenantID != tenant {
			return nil, fmt.Errorf("registry Agent at index %d belongs to a different tenant", index)
		}
	}
	return agents, nil
}

func (c *HTTPClient) Health(ctx context.Context) error {
	request, err := c.request(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return err
	}
	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("registry health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *HTTPClient) doJSON(ctx context.Context, method, path string, value any, target any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, err := c.request(ctx, method, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client().Do(request)
	if err != nil {
		return fmt.Errorf("registry request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("registry returned HTTP %d", response.StatusCode)
	}
	if target != nil {
		return decodeBody(response, target, c.MaxResponseBytes)
	}
	return nil
}

func (c *HTTPClient) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c == nil || c.Endpoint == "" {
		return nil, errors.New("registry client is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, body)
	if err != nil {
		return nil, err
	}
	if c.Bearer != nil {
		token, err := c.Bearer(ctx)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(token) == "" {
			return nil, errors.New("registry credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request, nil
}

func (c *HTTPClient) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

func decodeBody(response *http.Response, target any, limit int64) error {
	body, err := transport.ReadBounded(response.Body, limit)
	if err != nil {
		return fmt.Errorf("read registry response: %w", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}
