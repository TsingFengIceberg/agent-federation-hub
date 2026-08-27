package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

type PushDecoder func([]byte) (federation.Observation, error)

type HTTPHandler struct {
	Service           *Service
	DecodePush        PushDecoder
	MaxBodyBytes      int64
	Authenticator     Authenticator
	Authorizer        access.Authorizer
	Audit             access.AuditSink
	Now               func() time.Time
	EventPollInterval time.Duration
}

func (h *HTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agents", h.protected(access.ActionAgentRegister, h.registerAgent))
	mux.HandleFunc("GET /v1/agents", h.protected(access.ActionAgentList, h.listAgents))
	mux.HandleFunc("POST /v1/tasks", h.protected(access.ActionTaskSubmit, h.submitTask))
	mux.HandleFunc("GET /v1/tasks/{taskID}", h.protected(access.ActionTaskRead, h.getTask))
	mux.HandleFunc("GET /v1/tasks/{taskID}/events", h.protected(access.ActionTaskEvents, h.getEvents))
	mux.HandleFunc("POST /v1/tasks/{taskID}/cancel", h.protected(access.ActionTaskCancel, h.cancelTask))
	mux.HandleFunc("POST /v1/tasks/{taskID}/reconcile", h.protected(access.ActionTaskReconcile, h.reconcileTask))
	mux.HandleFunc("POST /v1/security/revocations", h.protected(access.ActionSecurityRevoke, h.revokeToken))
	mux.HandleFunc("GET /v1/artifacts/{artifactID}", h.protected(access.ActionArtifactRead, h.getArtifact))
	mux.HandleFunc("GET /v1/artifacts/{artifactID}/content", h.protected(access.ActionArtifactRead, h.getArtifactContent))
	mux.HandleFunc("POST /v1/tasks/{taskID}/push", h.push)
	return mux
}

func (h *HTTPHandler) getArtifact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	object, err := h.Service.GetArtifact(r.Context(), tenantID, r.PathValue("artifactID"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (h *HTTPHandler) getArtifactContent(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	if h.Service.Artifacts == nil {
		h.writeError(w, errors.New("artifact object storage is not configured"))
		return
	}
	reader, object, err := h.Service.Artifacts.Open(r.Context(), tenantID, r.PathValue("artifactID"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", object.DetectedMediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.Filename != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": object.Filename}))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (h *HTTPHandler) revokeToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	var input RevokeTokenInput
	if !h.decodeJSON(w, r, &input) {
		return
	}
	revocation, err := h.Service.RevokeToken(r.Context(), tenantID, input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, revocation)
}

func (h *HTTPHandler) protected(action access.Action, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = core.NewID()
		}
		w.Header().Set("X-Request-ID", requestID)
		if h.Authenticator == nil || h.Authorizer == nil {
			h.audit(r.Context(), access.AuditRecord{RequestID: requestID, Decision: "configuration_error", Action: action})
			writeProblem(w, http.StatusInternalServerError, "internal", "ACCESS_CONTROL_NOT_CONFIGURED", "Hub access control is not configured")
			return
		}
		principal, err := h.Authenticator.Authenticate(r.Context(), r)
		if err != nil {
			h.audit(r.Context(), access.AuditRecord{RequestID: requestID, Decision: "authentication_denied", Action: action, Reason: "invalid_or_missing_credential"})
			writeProblem(w, http.StatusUnauthorized, "authentication", "UNAUTHENTICATED", "valid authentication is required")
			return
		}
		audit := access.AuditRecord{
			RequestID: requestID, Decision: "authentication_allowed", Action: action,
			Subject: principal.Subject, TenantID: principal.TenantID,
			Issuer: principal.Issuer, AuthMethod: principal.AuthMethod,
		}
		h.audit(r.Context(), audit)
		resourceID := r.PathValue("taskID")
		if resourceID == "" {
			resourceID = r.PathValue("artifactID")
		}
		if err := h.Authorizer.Authorize(r.Context(), principal, access.Request{Action: action, ResourceID: resourceID}); err != nil {
			audit.Decision = "authorization_denied"
			audit.ResourceID = resourceID
			audit.Reason = "insufficient_scope_or_policy"
			h.audit(r.Context(), audit)
			writeProblem(w, http.StatusForbidden, "authorization", "FORBIDDEN", "the authenticated principal cannot perform this operation")
			return
		}
		audit.Decision = "authorization_allowed"
		audit.ResourceID = resourceID
		h.audit(r.Context(), audit)
		ctx := access.WithPrincipal(r.Context(), principal)
		next(w, r.WithContext(ctx))
	}
}

func (h *HTTPHandler) registerAgent(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	var input RegisterAgentInput
	if !h.decodeJSON(w, r, &input) {
		return
	}
	agent, err := h.Service.RegisterAgent(r.Context(), tenantID, input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

func (h *HTTPHandler) listAgents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	agents, err := h.Service.ListAgents(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (h *HTTPHandler) submitTask(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	var input SubmitTaskInput
	if !h.decodeJSON(w, r, &input) {
		return
	}
	if input.EnablePush && !h.requireAdditionalAuthorization(w, r, access.ActionPushConfigure) {
		return
	}
	task, err := h.Service.SubmitTask(r.Context(), tenantID, input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (h *HTTPHandler) getTask(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	task, err := h.Service.GetTask(r.Context(), tenantID, r.PathValue("taskID"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *HTTPHandler) getEvents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	afterValue := r.URL.Query().Get("after")
	if afterValue == "" {
		afterValue = r.Header.Get("Last-Event-ID")
	}
	var after uint64
	if afterValue != "" {
		parsed, err := strconv.ParseUint(afterValue, 10, 64)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "validation", "INVALID_CURSOR", "event cursor must be an unsigned integer")
			return
		}
		after = parsed
	}
	taskID := r.PathValue("taskID")
	events, err := h.Service.EventsAfter(r.Context(), tenantID, taskID, after)
	if err != nil {
		h.writeError(w, err)
		return
	}
	streaming := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if !streaming {
		writeJSON(w, http.StatusOK, events)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	follow := r.URL.Query().Get("follow") == "true"
	for {
		for _, event := range events {
			encoded, err := json.Marshal(event)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, encoded)
			after = event.Sequence
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if !follow {
			return
		}
		task, err := h.Service.GetTask(r.Context(), tenantID, taskID)
		if err != nil || (task.State.Terminal() && after >= task.LastSequence) {
			return
		}
		timer := time.NewTimer(h.eventPollInterval())
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		events, err = h.Service.EventsAfter(r.Context(), tenantID, taskID, after)
		if err != nil {
			return
		}
	}
}

func (h *HTTPHandler) cancelTask(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	task, err := h.Service.CancelTask(r.Context(), tenantID, r.PathValue("taskID"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *HTTPHandler) reconcileTask(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	task, err := h.Service.ReconcileTask(r.Context(), tenantID, r.PathValue("taskID"), false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *HTTPHandler) push(w http.ResponseWriter, r *http.Request) {
	if h.DecodePush == nil {
		writeProblem(w, http.StatusNotImplemented, "protocol", "PUSH_NOT_CONFIGURED", "Push decoding is not configured")
		return
	}
	tenantID := r.URL.Query().Get("tenant")
	if tenantID == "" {
		writeProblem(w, http.StatusBadRequest, "validation", "TENANT_REQUIRED", "Push callback tenant is required")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeProblem(w, http.StatusUnauthorized, "authentication", "PUSH_CREDENTIAL_REQUIRED", "Push Bearer credential is required")
		return
	}
	limit := h.maxBodyBytes()
	payload, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "validation", "INVALID_BODY", "request body could not be read")
		return
	}
	if int64(len(payload)) > limit {
		writeProblem(w, http.StatusRequestEntityTooLarge, "validation", "PAYLOAD_TOO_LARGE", "Push payload exceeds the configured limit")
		return
	}
	observation, err := h.DecodePush(payload)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if _, err := h.Service.AcceptPush(r.Context(), tenantID, r.PathValue("taskID"), token, observation); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, h.maxBodyBytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "validation", "INVALID_JSON", "request body must be one valid JSON object")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "validation", "INVALID_JSON", "request body must contain only one JSON object")
		return false
	}
	return true
}

func (h *HTTPHandler) maxBodyBytes() int64 {
	if h.MaxBodyBytes > 0 {
		return h.MaxBodyBytes
	}
	return 1 << 20
}

func (h *HTTPHandler) eventPollInterval() time.Duration {
	if h.EventPollInterval > 0 {
		return h.EventPollInterval
	}
	return 250 * time.Millisecond
}

func (h *HTTPHandler) requireAdditionalAuthorization(w http.ResponseWriter, r *http.Request, action access.Action) bool {
	principal, ok := access.PrincipalFromContext(r.Context())
	if !ok || h.Authorizer.Authorize(r.Context(), principal, access.Request{Action: action}) != nil {
		h.audit(r.Context(), access.AuditRecord{
			Timestamp: h.now(), RequestID: w.Header().Get("X-Request-ID"), Decision: "authorization_denied",
			Action: action, Subject: principal.Subject, TenantID: principal.TenantID,
			Issuer: principal.Issuer, AuthMethod: principal.AuthMethod, Reason: "insufficient_scope_or_policy",
		})
		writeProblem(w, http.StatusForbidden, "authorization", "FORBIDDEN", "the authenticated principal cannot perform this operation")
		return false
	}
	h.audit(r.Context(), access.AuditRecord{
		Timestamp: h.now(), RequestID: w.Header().Get("X-Request-ID"), Decision: "authorization_allowed",
		Action: action, Subject: principal.Subject, TenantID: principal.TenantID,
		Issuer: principal.Issuer, AuthMethod: principal.AuthMethod,
	})
	return true
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	category, code, message := "internal", "HUB_OPERATION_FAILED", "Hub operation failed"
	var adapterErr *federation.Error
	switch {
	case errors.Is(err, core.ErrNotFound):
		status, category, code, message = http.StatusNotFound, "resource", "NOT_FOUND", "resource not found"
	case errors.Is(err, core.ErrConflict):
		status, category, code, message = http.StatusConflict, "state", "CONFLICT", "resource already exists"
	case errors.Is(err, core.ErrQuotaExceeded):
		status, category, code, message = http.StatusTooManyRequests, "quota", "ARTIFACT_QUOTA_EXCEEDED", "tenant Artifact quota is exhausted"
	case errors.Is(err, artifactstore.ErrUnavailable):
		status, category, code, message = http.StatusNotFound, "resource", "ARTIFACT_UNAVAILABLE", "Artifact content is unavailable"
	case errors.Is(err, artifactstore.ErrPolicy):
		status, category, code, message = http.StatusUnprocessableEntity, "validation", "ARTIFACT_POLICY_REJECTED", "Artifact content violates policy"
	case errors.Is(err, ErrInvalidPushCredential):
		status, category, code, message = http.StatusUnauthorized, "authentication", "INVALID_PUSH_CREDENTIAL", "Push credential is invalid"
	case errors.Is(err, ErrPushTaskMismatch):
		status, category, code, message = http.StatusConflict, "state", "PUSH_TASK_MISMATCH", "Push task does not match callback task"
	case errors.As(err, &adapterErr):
		category, code, message = adapterErr.Problem.Category, adapterErr.Problem.Code, adapterErr.Problem.Message
		if category == "authentication" {
			status = http.StatusUnauthorized
		} else if category == "authorization" {
			status = http.StatusForbidden
		} else if category == "validation" || category == "protocol" {
			status = http.StatusBadRequest
		} else if category == "resource" {
			status = http.StatusNotFound
		} else if category == "state" {
			status = http.StatusConflict
		}
	case strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "invalid"),
		strings.Contains(err.Error(), "does not declare"), strings.Contains(err.Error(), "not allowed"):
		status, category, code, message = http.StatusBadRequest, "validation", "INVALID_REQUEST", err.Error()
	}
	writeProblem(w, status, category, code, message)
}

func requireTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal, ok := access.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		writeProblem(w, http.StatusUnauthorized, "authentication", "UNAUTHENTICATED", "authenticated tenant identity is required")
		return "", false
	}
	return principal.TenantID, true
}

func (h *HTTPHandler) audit(ctx context.Context, record access.AuditRecord) {
	if record.Timestamp.IsZero() {
		record.Timestamp = h.now()
	}
	sink := h.Audit
	if sink == nil {
		sink = access.NopAuditSink{}
	}
	_ = sink.Record(ctx, record)
}

func (h *HTTPHandler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func writeProblem(w http.ResponseWriter, status int, category, code, message string) {
	writeJSON(w, status, core.Problem{Category: category, Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if task, ok := value.(core.Task); ok {
		task.PushTokenHash = ""
		value = task
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
