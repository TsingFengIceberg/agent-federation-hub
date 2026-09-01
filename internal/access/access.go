package access

import (
	"context"
	"errors"
	"slices"
	"time"
)

var (
	ErrUnauthenticated = errors.New("request is not authenticated")
	ErrForbidden       = errors.New("request is not authorized")
)

type DelegatedActor struct {
	Subject string `json:"subject"`
	Issuer  string `json:"issuer,omitempty"`
}

type Principal struct {
	Subject    string           `json:"subject"`
	TenantID   string           `json:"tenantId"`
	Issuer     string           `json:"issuer"`
	AuthMethod string           `json:"authMethod"`
	Scopes     []string         `json:"scopes,omitempty"`
	Roles      []string         `json:"roles,omitempty"`
	Delegation []DelegatedActor `json:"delegation,omitempty"`
	TokenID    string           `json:"-"`
}

func (p Principal) HasScope(scope string) bool {
	return slices.Contains(p.Scopes, "*") || slices.Contains(p.Scopes, scope)
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

type Action string

const (
	ActionAgentRegister  Action = "agent.register"
	ActionAgentList      Action = "agent.list"
	ActionAgentRefresh   Action = "agent.refresh"
	ActionOutboxList     Action = "outbox.list"
	ActionOutboxReplay   Action = "outbox.replay"
	ActionOutboxPurge    Action = "outbox.purge"
	ActionTaskSubmit     Action = "task.submit"
	ActionTaskContinue   Action = "task.continue"
	ActionTaskRead       Action = "task.read"
	ActionTaskEvents     Action = "task.events"
	ActionTaskCancel     Action = "task.cancel"
	ActionTaskReconcile  Action = "task.reconcile"
	ActionPushConfigure  Action = "push.configure"
	ActionSecurityRevoke Action = "security.revoke"
	ActionArtifactRead   Action = "artifact.read"
)

type Request struct {
	Action     Action
	ResourceID string
}

type Authorizer interface {
	Authorize(context.Context, Principal, Request) error
}

type ScopeAuthorizer struct {
	Required       map[Action]string
	RoleScopes     map[string]map[string]struct{}
	TenantRequired map[string]map[Action]string
}

func DefaultScopeAuthorizer() *ScopeAuthorizer {
	return &ScopeAuthorizer{Required: map[Action]string{
		ActionAgentRegister:  "agents:write",
		ActionAgentList:      "agents:read",
		ActionAgentRefresh:   "agents:write",
		ActionOutboxList:     "outbox:read",
		ActionOutboxReplay:   "outbox:write",
		ActionOutboxPurge:    "outbox:write",
		ActionTaskSubmit:     "tasks:submit",
		ActionTaskContinue:   "tasks:continue",
		ActionTaskRead:       "tasks:read",
		ActionTaskEvents:     "tasks:read",
		ActionTaskCancel:     "tasks:cancel",
		ActionTaskReconcile:  "tasks:reconcile",
		ActionPushConfigure:  "push:configure",
		ActionSecurityRevoke: "security:revoke",
		ActionArtifactRead:   "artifacts:read",
	}, RoleScopes: make(map[string]map[string]struct{}), TenantRequired: make(map[string]map[Action]string)}
}

func (a *ScopeAuthorizer) Authorize(_ context.Context, principal Principal, request Request) error {
	if principal.Subject == "" || principal.TenantID == "" {
		return ErrUnauthenticated
	}
	required, ok := a.Required[request.Action]
	if tenantRules, exists := a.TenantRequired[principal.TenantID]; exists {
		if tenantRequired, configured := tenantRules[request.Action]; configured {
			required, ok = tenantRequired, tenantRequired != ""
		}
	}
	if !ok || !a.hasScope(principal, required) {
		return ErrForbidden
	}
	return nil
}

func (a *ScopeAuthorizer) hasScope(principal Principal, required string) bool {
	if principal.HasScope(required) {
		return true
	}
	for _, role := range principal.Roles {
		if scopes, ok := a.RoleScopes[role]; ok {
			if _, found := scopes[required]; found {
				return true
			}
			if _, found := scopes["*"]; found {
				return true
			}
		}
	}
	return false
}

type AuditRecord struct {
	Version       int       `json:"version,omitempty"`
	Sequence      uint64    `json:"sequence,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	RequestID     string    `json:"requestId"`
	Decision      string    `json:"decision"`
	Action        Action    `json:"action,omitempty"`
	Subject       string    `json:"subject,omitempty"`
	TenantID      string    `json:"tenantId,omitempty"`
	Issuer        string    `json:"issuer,omitempty"`
	AuthMethod    string    `json:"authMethod,omitempty"`
	ResourceID    string    `json:"resourceId,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	PreviousHash  string    `json:"previousHash,omitempty"`
	IntegrityHash string    `json:"integrityHash,omitempty"`
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type NopAuditSink struct{}

func (NopAuditSink) Record(context.Context, AuditRecord) error { return nil }
