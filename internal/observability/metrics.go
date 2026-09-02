package observability

import (
	"fmt"
	"sync/atomic"
)

// Metrics is a process-local instrumentation surface. Deployments can expose
// Prometheus() to a scraper and attach the same counters to a durable metrics
// backend; no provider payload or prompt is stored in labels.
type Metrics struct {
	HTTPRequests       atomic.Uint64
	HTTPAuthDenied     atomic.Uint64
	HTTPAuthzDenied    atomic.Uint64
	TaskSubmitted      atomic.Uint64
	TaskAcknowledged   atomic.Uint64
	TaskCompleted      atomic.Uint64
	TaskFailed         atomic.Uint64
	TaskCanceled       atomic.Uint64
	WorkflowStarted    atomic.Uint64
	WorkflowCompleted  atomic.Uint64
	WorkflowFailed     atomic.Uint64
	WorkerLeaseClaims  atomic.Uint64
	WorkerLeaseExpired atomic.Uint64
	WorkerErrors       atomic.Uint64
}

func (m *Metrics) IncHTTPRequest() {
	if m != nil {
		m.HTTPRequests.Add(1)
	}
}
func (m *Metrics) IncHTTPAuthDenied() {
	if m != nil {
		m.HTTPAuthDenied.Add(1)
	}
}
func (m *Metrics) IncHTTPAuthzDenied() {
	if m != nil {
		m.HTTPAuthzDenied.Add(1)
	}
}
func (m *Metrics) IncTaskSubmitted() {
	if m != nil {
		m.TaskSubmitted.Add(1)
	}
}
func (m *Metrics) IncTaskAcknowledged() {
	if m != nil {
		m.TaskAcknowledged.Add(1)
	}
}
func (m *Metrics) IncTaskState(state string) {
	if m == nil {
		return
	}
	switch state {
	case "COMPLETED":
		m.TaskCompleted.Add(1)
	case "FAILED", "REJECTED":
		m.TaskFailed.Add(1)
	case "CANCELED":
		m.TaskCanceled.Add(1)
	}
}
func (m *Metrics) IncWorkflowStarted() {
	if m != nil {
		m.WorkflowStarted.Add(1)
	}
}
func (m *Metrics) IncWorkflowState(state string) {
	if m == nil {
		return
	}
	switch state {
	case "COMPLETED", "COMPENSATED":
		m.WorkflowCompleted.Add(1)
	case "FAILED", "PARTIALLY_FAILED":
		m.WorkflowFailed.Add(1)
	}
}
func (m *Metrics) IncWorkerLeaseClaim() {
	if m != nil {
		m.WorkerLeaseClaims.Add(1)
	}
}
func (m *Metrics) IncWorkerLeaseExpired() {
	if m != nil {
		m.WorkerLeaseExpired.Add(1)
	}
}
func (m *Metrics) IncWorkerError() {
	if m != nil {
		m.WorkerErrors.Add(1)
	}
}

func (m *Metrics) Prometheus() string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf(
		"# HELP afh_http_requests_total HTTP requests accepted by the Hub.\n# TYPE afh_http_requests_total counter\nafh_http_requests_total %d\n"+
			"# HELP afh_http_auth_denied_total HTTP requests rejected during authentication.\n# TYPE afh_http_auth_denied_total counter\nafh_http_auth_denied_total %d\n"+
			"# HELP afh_http_authorization_denied_total HTTP requests rejected during authorization.\n# TYPE afh_http_authorization_denied_total counter\nafh_http_authorization_denied_total %d\n"+
			"# HELP afh_tasks_submitted_total Hub Tasks submitted.\n# TYPE afh_tasks_submitted_total counter\nafh_tasks_submitted_total %d\n"+
			"# HELP afh_tasks_acknowledged_total remote Tasks acknowledged.\n# TYPE afh_tasks_acknowledged_total counter\nafh_tasks_acknowledged_total %d\n"+
			"# HELP afh_tasks_completed_total Tasks completed.\n# TYPE afh_tasks_completed_total counter\nafh_tasks_completed_total %d\n"+
			"# HELP afh_tasks_failed_total Tasks failed or rejected.\n# TYPE afh_tasks_failed_total counter\nafh_tasks_failed_total %d\n"+
			"# HELP afh_tasks_canceled_total Tasks canceled.\n# TYPE afh_tasks_canceled_total counter\nafh_tasks_canceled_total %d\n"+
			"# HELP afh_workflows_started_total Workflows started.\n# TYPE afh_workflows_started_total counter\nafh_workflows_started_total %d\n"+
			"# HELP afh_workflows_completed_total Workflows completed or compensated.\n# TYPE afh_workflows_completed_total counter\nafh_workflows_completed_total %d\n"+
			"# HELP afh_workflows_failed_total Workflows failed.\n# TYPE afh_workflows_failed_total counter\nafh_workflows_failed_total %d\n"+
			"# HELP afh_worker_lease_claims_total Worker leases claimed.\n# TYPE afh_worker_lease_claims_total counter\nafh_worker_lease_claims_total %d\n"+
			"# HELP afh_worker_lease_expired_total Worker leases expired or were taken over.\n# TYPE afh_worker_lease_expired_total counter\nafh_worker_lease_expired_total %d\n"+
			"# HELP afh_worker_errors_total Background worker errors.\n# TYPE afh_worker_errors_total counter\nafh_worker_errors_total %d\n",
		m.HTTPRequests.Load(), m.HTTPAuthDenied.Load(), m.HTTPAuthzDenied.Load(),
		m.TaskSubmitted.Load(), m.TaskAcknowledged.Load(), m.TaskCompleted.Load(),
		m.TaskFailed.Load(), m.TaskCanceled.Load(), m.WorkflowStarted.Load(),
		m.WorkflowCompleted.Load(), m.WorkflowFailed.Load(), m.WorkerLeaseClaims.Load(),
		m.WorkerLeaseExpired.Load(), m.WorkerErrors.Load())
}
