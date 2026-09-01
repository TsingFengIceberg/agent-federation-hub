package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type ChainAuthorizer []Authorizer

// PolicyDocument is the versioned, operator-owned local authorization policy.
// It maps roles to scopes and can override required scopes per tenant. External
// PDPs remain available for richer resource or consent decisions.
type PolicyDocument struct {
	Version       int                          `json:"version"`
	Actions       map[string]string            `json:"actions,omitempty"`
	Roles         map[string][]string          `json:"roles,omitempty"`
	TenantActions map[string]map[string]string `json:"tenantActions,omitempty"`
}

// LoadPolicyFile parses a strict JSON policy document and rejects unknown
// actions, empty role scopes, and unsupported versions before the Hub starts.
func LoadPolicyFile(path string) (PolicyDocument, error) {
	if strings.TrimSpace(path) == "" {
		return PolicyDocument{}, errors.New("policy file path is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return PolicyDocument{}, fmt.Errorf("read policy file: %w", err)
	}
	if len(encoded) > 1<<20 {
		return PolicyDocument{}, errors.New("policy file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document PolicyDocument
	if err := decoder.Decode(&document); err != nil {
		return PolicyDocument{}, fmt.Errorf("decode policy file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PolicyDocument{}, errors.New("policy file contains trailing data")
	}
	if err := document.Validate(); err != nil {
		return PolicyDocument{}, err
	}
	return document, nil
}

func (d PolicyDocument) Validate() error {
	if d.Version != 1 {
		return errors.New("policy version must be 1")
	}
	for action, scope := range d.Actions {
		if !knownAction(Action(action)) {
			return fmt.Errorf("policy action %q is not supported", action)
		}
		if strings.TrimSpace(scope) == "" {
			return fmt.Errorf("policy action %q has an empty scope", action)
		}
	}
	for role, scopes := range d.Roles {
		if strings.TrimSpace(role) == "" || len(scopes) == 0 {
			return errors.New("policy roles must have a name and at least one scope")
		}
		for _, scope := range scopes {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf("policy role %q contains an empty scope", role)
			}
		}
	}
	for tenant, actions := range d.TenantActions {
		if strings.TrimSpace(tenant) == "" {
			return errors.New("policy tenant IDs must not be empty")
		}
		for action, scope := range actions {
			if !knownAction(Action(action)) || strings.TrimSpace(scope) == "" {
				return fmt.Errorf("policy tenant %q contains an invalid action or scope", tenant)
			}
		}
	}
	return nil
}

func knownAction(action Action) bool {
	switch action {
	case ActionAgentRegister, ActionAgentList, ActionAgentRefresh,
		ActionOutboxList, ActionOutboxReplay, ActionOutboxPurge,
		ActionTaskSubmit, ActionTaskContinue, ActionTaskRead, ActionTaskEvents,
		ActionTaskCancel, ActionTaskReconcile, ActionPushConfigure,
		ActionSecurityRevoke, ActionArtifactRead:
		return true
	default:
		return false
	}
}

// NewPolicyAuthorizer overlays a validated document on the default action
// contract. Unspecified actions retain their default required scopes.
func NewPolicyAuthorizer(document PolicyDocument) (*ScopeAuthorizer, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	authorizer := DefaultScopeAuthorizer()
	for action, scope := range document.Actions {
		authorizer.Required[Action(action)] = scope
	}
	for role, scopes := range document.Roles {
		set := make(map[string]struct{}, len(scopes))
		for _, scope := range scopes {
			set[scope] = struct{}{}
		}
		authorizer.RoleScopes[role] = set
	}
	for tenant, actions := range document.TenantActions {
		if authorizer.TenantRequired[tenant] == nil {
			authorizer.TenantRequired[tenant] = make(map[Action]string)
		}
		for action, scope := range actions {
			authorizer.TenantRequired[tenant][Action(action)] = scope
		}
	}
	return authorizer, nil
}

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
