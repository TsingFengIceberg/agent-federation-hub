// Package gateway provides an optional policy/data-plane gateway adapter.
// Direct A2A remains the default; this adapter is selected explicitly when an
// operator requires centralized egress policy, audit, or network mediation.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

type HTTPAdapter struct {
	Endpoint string
	Client   *http.Client
	Bearer   func(context.Context) (string, error)
	Direct   federation.Adapter
}

// Request is the stable JSON contract between a policy gateway and its Hub
// adapter. It intentionally carries the opaque Agent descriptor and A2A
// message, not provider internals.
type Request struct {
	Agent        core.Agent         `json:"agent"`
	Message      federation.Message `json:"message,omitempty"`
	RemoteTaskID string             `json:"remoteTaskId,omitempty"`
}

// Handler turns any federation.Adapter into a minimal gateway data plane. A
// real deployment should wrap it with its own mTLS, policy, rate limiting and
// audit middleware; the handler keeps those concerns out of the protocol core.
type Handler struct{ Adapter federation.Adapter }

func (h Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if h.Adapter == nil {
		http.Error(response, "gateway adapter is not configured", http.StatusInternalServerError)
		return
	}
	operation := strings.TrimPrefix(request.URL.Path, "/v1/proxy/")
	if operation != "send" && operation != "get" && operation != "cancel" && operation != "subscribe" {
		http.NotFound(response, request)
		return
	}
	var input Request
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20)).Decode(&input); err != nil {
		http.Error(response, "invalid gateway request", http.StatusBadRequest)
		return
	}
	var observations []federation.Observation
	var err error
	switch operation {
	case "send":
		for observation, sendErr := range h.Adapter.Send(request.Context(), input.Agent, input.Message) {
			if sendErr != nil {
				err = sendErr
				break
			}
			observations = append(observations, observation)
		}
	case "get":
		var observation federation.Observation
		observation, err = h.Adapter.Get(request.Context(), input.Agent, input.RemoteTaskID)
		observations = append(observations, observation)
	case "cancel":
		var observation federation.Observation
		observation, err = h.Adapter.Cancel(request.Context(), input.Agent, input.RemoteTaskID)
		observations = append(observations, observation)
	case "subscribe":
		for observation, subscribeErr := range h.Adapter.Subscribe(request.Context(), input.Agent, input.RemoteTaskID) {
			if subscribeErr != nil {
				err = subscribeErr
				break
			}
			observations = append(observations, observation)
		}
	}
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadGateway)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(observations)
}

func NewHTTPAdapter(endpoint string, direct federation.Adapter, bearer func(context.Context) (string, error)) (*HTTPAdapter, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("gateway endpoint must be an HTTPS URL without user information")
	}
	if direct == nil {
		return nil, errors.New("direct discovery adapter is required")
	}
	return &HTTPAdapter{Endpoint: strings.TrimRight(parsed.String(), "/"), Direct: direct, Client: &http.Client{Timeout: 30 * time.Second}, Bearer: bearer}, nil
}

func (a *HTTPAdapter) Discover(ctx context.Context, cardURL string) (federation.Descriptor, error) {
	return a.Direct.Discover(ctx, cardURL)
}

func (a *HTTPAdapter) Send(ctx context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return a.call(ctx, "send", gatewayRequest{Agent: agent, Message: message})
}

func (a *HTTPAdapter) Get(ctx context.Context, agent core.Agent, taskID string) (federation.Observation, error) {
	return a.single(ctx, "get", gatewayRequest{Agent: agent, RemoteTaskID: taskID})
}

func (a *HTTPAdapter) Cancel(ctx context.Context, agent core.Agent, taskID string) (federation.Observation, error) {
	return a.single(ctx, "cancel", gatewayRequest{Agent: agent, RemoteTaskID: taskID})
}

func (a *HTTPAdapter) Subscribe(ctx context.Context, agent core.Agent, taskID string) iter.Seq2[federation.Observation, error] {
	return a.call(ctx, "subscribe", gatewayRequest{Agent: agent, RemoteTaskID: taskID})
}

type gatewayRequest = Request

func (a *HTTPAdapter) call(ctx context.Context, operation string, input gatewayRequest) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		observations, err := a.request(ctx, operation, input)
		if err != nil {
			yield(federation.Observation{}, err)
			return
		}
		for _, observation := range observations {
			if !yield(observation, nil) {
				return
			}
		}
	}
}

func (a *HTTPAdapter) single(ctx context.Context, operation string, input gatewayRequest) (federation.Observation, error) {
	observations, err := a.request(ctx, operation, input)
	if err != nil {
		return federation.Observation{}, err
	}
	if len(observations) == 0 {
		return federation.Observation{}, errors.New("gateway returned no observation")
	}
	return observations[0], nil
}

func (a *HTTPAdapter) request(ctx context.Context, operation string, input gatewayRequest) ([]federation.Observation, error) {
	if a == nil || a.Endpoint == "" {
		return nil, errors.New("gateway adapter is not configured")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint+"/v1/proxy/"+url.PathEscape(operation), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AFH-Gateway-Operation", operation)
	if a.Bearer != nil {
		token, err := a.Bearer(ctx)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(token) == "" {
			return nil, errors.New("gateway credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("gateway request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway returned HTTP %d", response.StatusCode)
	}
	var observations []federation.Observation
	if err := json.NewDecoder(response.Body).Decode(&observations); err != nil {
		return nil, fmt.Errorf("decode gateway observations: %w", err)
	}
	return observations, nil
}
