package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

const TenantHeader = "X-AFH-Tenant-ID"

type PushDecoder func([]byte) (federation.Observation, error)

type HTTPHandler struct {
	Service      *Service
	DecodePush   PushDecoder
	MaxBodyBytes int64
}

func (h *HTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agents", h.registerAgent)
	mux.HandleFunc("GET /v1/agents", h.listAgents)
	mux.HandleFunc("POST /v1/tasks", h.submitTask)
	mux.HandleFunc("GET /v1/tasks/{taskID}", h.getTask)
	mux.HandleFunc("GET /v1/tasks/{taskID}/events", h.getEvents)
	mux.HandleFunc("POST /v1/tasks/{taskID}/cancel", h.cancelTask)
	mux.HandleFunc("POST /v1/tasks/{taskID}/reconcile", h.reconcileTask)
	mux.HandleFunc("POST /v1/tasks/{taskID}/push", h.push)
	return mux
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
	events, err := h.Service.EventsAfter(r.Context(), tenantID, r.PathValue("taskID"), after)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		writeJSON(w, http.StatusOK, events)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, encoded)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
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

func (h *HTTPHandler) writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	category, code, message := "internal", "HUB_OPERATION_FAILED", "Hub operation failed"
	var adapterErr *federation.Error
	switch {
	case errors.Is(err, core.ErrNotFound):
		status, category, code, message = http.StatusNotFound, "resource", "NOT_FOUND", "resource not found"
	case errors.Is(err, core.ErrConflict):
		status, category, code, message = http.StatusConflict, "state", "CONFLICT", "resource already exists"
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
	tenantID := strings.TrimSpace(r.Header.Get(TenantHeader))
	if tenantID == "" {
		writeProblem(w, http.StatusBadRequest, "validation", "TENANT_REQUIRED", TenantHeader+" is required")
		return "", false
	}
	return tenantID, true
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
