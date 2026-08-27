package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ChainAuthorizer []Authorizer

func (chain ChainAuthorizer) Authorize(ctx context.Context, principal Principal, request Request) error {
	for _, authorizer := range chain {
		if authorizer == nil {
			return fmt.Errorf("%w: policy chain contains an unconfigured authorizer", ErrForbidden)
		}
		if err := authorizer.Authorize(ctx, principal, request); err != nil {
			return err
		}
	}
	return nil
}

type BearerProvider interface {
	Resolve(context.Context, string) (string, error)
}

type HTTPAuthorizer struct {
	Endpoint         string
	Client           *http.Client
	Bearer           BearerProvider
	BearerReference  string
	MaxResponseBytes int64
}

type policyRequest struct {
	Principal Principal `json:"principal"`
	Request   Request   `json:"request"`
}

type policyResponse struct {
	Allow      bool   `json:"allow"`
	DecisionID string `json:"decisionId,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (a *HTTPAuthorizer) Authorize(ctx context.Context, principal Principal, request Request) error {
	if a.Endpoint == "" || !strings.HasPrefix(a.Endpoint, "https://") {
		return fmt.Errorf("%w: external policy endpoint is not configured with HTTPS", ErrForbidden)
	}
	payload, err := json.Marshal(policyRequest{Principal: principal, Request: request})
	if err != nil {
		return fmt.Errorf("%w: encode policy input", ErrForbidden)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: create policy request", ErrForbidden)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if a.BearerReference != "" {
		if a.Bearer == nil {
			return fmt.Errorf("%w: policy credential provider is unavailable", ErrForbidden)
		}
		credential, err := a.Bearer.Resolve(ctx, a.BearerReference)
		if err != nil {
			return fmt.Errorf("%w: policy credential is unavailable", ErrForbidden)
		}
		httpRequest.Header.Set("Authorization", "Bearer "+credential)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("%w: external policy request failed", ErrForbidden)
	}
	defer response.Body.Close()
	limit := a.MaxResponseBytes
	if limit <= 0 {
		limit = 64 << 10
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit || response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: external policy response is unavailable", ErrForbidden)
	}
	var decision policyResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return fmt.Errorf("%w: external policy response is invalid", ErrForbidden)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: external policy response contains trailing data", ErrForbidden)
	}
	if !decision.Allow {
		return fmt.Errorf("%w: external policy denied the operation", ErrForbidden)
	}
	return nil
}
