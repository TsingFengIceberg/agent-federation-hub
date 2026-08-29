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
)

type Client interface {
	Register(context.Context, core.Agent) error
	List(context.Context, string) ([]core.Agent, error)
	Health(context.Context) error
}

type HTTPClient struct {
	Endpoint string
	Client   *http.Client
	Bearer   func(context.Context) (string, error)
}

func NewHTTPClient(endpoint string, bearer func(context.Context) (string, error)) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("registry endpoint must be an HTTPS URL without user information")
	}
	return &HTTPClient{Endpoint: strings.TrimRight(parsed.String(), "/"), Client: &http.Client{Timeout: 10 * time.Second}, Bearer: bearer}, nil
}

func (c *HTTPClient) Register(ctx context.Context, agent core.Agent) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/agents", agent, nil)
}

func (c *HTTPClient) List(ctx context.Context, tenant string) ([]core.Agent, error) {
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
	if err := decodeBody(response, &agents); err != nil {
		return nil, err
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
		return decodeBody(response, target)
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

func decodeBody(response *http.Response, target any) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("registry response exceeds size limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}
