package preflight

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunReportsValidLocalConfiguration(t *testing.T) {
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(agentPath, []byte(`schema_version: 1
defaults:
  protocol:
    kind: a2a
    profiles:
      - protocol_version: "1.0"
        binding: JSONRPC
        stream_transport: SSE
  discovery:
    max_card_bytes: 1024
  execution:
    remote_timeout_seconds: 1
  limits:
    max_concurrent_tasks: 1
agents: []
`), 0o600); err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(dir, "trust.json")
	if err := os.WriteFile(trustPath, []byte(`{"version":1,"generation":1,"notBefore":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z","issuers":{"https://idp.example":{"tenants":["tenant-a"],"requiredScopes":[]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"version":1,"roles":{"operator":["agents:read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	matrixPath := filepath.Join(dir, "matrix.json")
	if err := os.WriteFile(matrixPath, []byte(`{"protocolVersion":"1.0","profiles":[{"binding":"JSONRPC","status":"partial","tckMustFailed":0}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Run(Options{AgentConfigPath: agentPath, TrustBundlePath: trustPath, AccessPolicyPath: policyPath, ProfileMatrix: matrixPath, AuthMode: "development", Now: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)})
	if !report.Passed {
		t.Fatalf("report=%+v", report)
	}
}

func TestRunFailsClosedForProductionWithoutTLS(t *testing.T) {
	report := Run(Options{AuthMode: "oidc"})
	if report.Passed {
		t.Fatalf("production preflight unexpectedly passed: %+v", report)
	}
}

func TestRunRejectsTrailingProfileMatrixData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.json")
	if err := os.WriteFile(path, []byte(`{"profiles":[{"binding":"JSONRPC","status":"partial","tckMustFailed":0}]} {"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := checkProfileMatrix(path)
	if len(checks) != 1 || checks[0].Status != "failed" {
		t.Fatalf("checks=%+v", checks)
	}
}
