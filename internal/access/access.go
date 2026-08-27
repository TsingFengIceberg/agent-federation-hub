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
	ActionTaskSubmit     Action = "task.submit"
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
	Required map[Action]string
}

func DefaultScopeAuthorizer() *ScopeAuthorizer {
	return &ScopeAuthorizer{Required: map[Action]string{
		ActionAgentRegister:  "agents:write",
		ActionAgentList:      "agents:read",
		ActionTaskSubmit:     "tasks:submit",
		ActionTaskRead:       "tasks:read",
		ActionTaskEvents:     "tasks:read",
		ActionTaskCancel:     "tasks:cancel",
		ActionTaskReconcile:  "tasks:reconcile",
		ActionPushConfigure:  "push:configure",
		ActionSecurityRevoke: "security:revoke",
		ActionArtifactRead:   "artifacts:read",
	}}
}

func (a *ScopeAuthorizer) Authorize(_ context.Context, principal Principal, request Request) error {
	if principal.Subject == "" || principal.TenantID == "" {
		return ErrUnauthenticated
	}
	required, ok := a.Required[request.Action]
	if !ok || !principal.HasScope(required) {
		return ErrForbidden
	}
	return nil
}

type AuditRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"requestId"`
	Decision   string    `json:"decision"`
	Action     Action    `json:"action,omitempty"`
	Subject    string    `json:"subject,omitempty"`
	TenantID   string    `json:"tenantId,omitempty"`
	Issuer     string    `json:"issuer,omitempty"`
	AuthMethod string    `json:"authMethod,omitempty"`
	ResourceID string    `json:"resourceId,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type NopAuditSink struct{}

func (NopAuditSink) Record(context.Context, AuditRecord) error { return nil }
