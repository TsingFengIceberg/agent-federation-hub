package observability

import (
	"context"
	"strings"
	"testing"
)

func TestMetricsExposePlatformCountersWithoutLabels(t *testing.T) {
	m := &Metrics{}
	m.IncHTTPRequest()
	m.IncTaskSubmitted()
	m.IncTaskState("COMPLETED")
	m.IncWorkflowState("FAILED")
	output := m.Prometheus()
	for _, line := range []string{"afh_http_requests_total 1", "afh_tasks_submitted_total 1", "afh_tasks_completed_total 1", "afh_workflows_failed_total 1"} {
		if !strings.Contains(output, line) {
			t.Fatalf("metrics missing %q", line)
		}
	}
	if strings.Contains(output, "tenant=") || strings.Contains(output, "prompt") {
		t.Fatal("metrics expose unsafe labels")
	}
}

func TestCorrelationIsCopiedAndNoopTracerIsSafe(t *testing.T) {
	ctx := WithCorrelation(context.Background(), map[string]string{"task.id": "task-a"})
	values := Correlation(ctx)
	values["task.id"] = "changed"
	if Correlation(ctx)["task.id"] != "task-a" {
		t.Fatal("correlation map was not copied")
	}
	spanCtx, span := NoopTracer().Start(ctx, "task.submit", values)
	span.SetAttribute("x", "y")
	span.End(nil)
	if len(Correlation(spanCtx)) != 1 {
		t.Fatal("tracer changed correlation context")
	}
}
