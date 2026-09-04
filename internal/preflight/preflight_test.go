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
	if err := os.WriteFile(matrixPath, []byte(validProfileMatrixJSON), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte(validProfileMatrixJSON+` {"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := checkProfileMatrix(path)
	if len(checks) != 1 || checks[0].Status != "failed" {
		t.Fatalf("checks=%+v", checks)
	}
}

const validProfileMatrixJSON = `{"schemaVersion":1,"protocolVersion":"1.0","protocolSourceCommit":"0123456789abcdef0123456789abcdef01234567","goSDKModule":"github.com/a2aproject/a2a-go/v2","goSDKVersion":"v2.5.0","tckCommit":"1123456789abcdef0123456789abcdef01234567","tckProtocolCommit":"2123456789abcdef0123456789abcdef01234567","profiles":[{"name":"jsonrpc","binding":"JSONRPC","streamTransport":"SSE","status":"partial","tckMustPassed":1,"tckTransportPassed":1}]}`

func TestRunRejectsUnpairedTrustBundleSignatureFiles(t *testing.T) {
	report := Run(Options{
		TrustBundlePath:          "trust.json",
		TrustBundleSignaturePath: "trust.sig",
		AuthMode:                 "development",
	})
	if report.Passed {
		t.Fatalf("unpaired Trust Bundle signature unexpectedly passed: %+v", report)
	}
	for _, check := range report.Checks {
		if check.ID == "trust-bundle-signature" && check.Status == "failed" {
			return
		}
	}
	t.Fatalf("missing trust-bundle-signature failure: %+v", report.Checks)
}

func TestRunValidatesRemoteTrustBundleShapeWithoutFetching(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "bundle.pub")
	if err := os.WriteFile(keyPath, []byte("public-key-placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Run(Options{
		TrustBundleURL:              "https://trust.example/bundle",
		TrustBundleSignatureURL:     "https://trust.example/bundle.sig",
		TrustBundleSignatureKeyPath: keyPath,
		AuthMode:                    "development",
	})
	if !report.Passed {
		t.Fatalf("remote Trust Bundle shape unexpectedly failed: %+v", report)
	}
	bad := Run(Options{TrustBundleURL: "http://trust.example/bundle", TrustBundleSignatureURL: "https://trust.example/bundle.sig", TrustBundleSignatureKeyPath: keyPath})
	if bad.Passed {
		t.Fatalf("HTTP remote Trust Bundle unexpectedly passed: %+v", bad)
	}
	crossOrigin := Run(Options{TrustBundleURL: "https://trust.example/bundle", TrustBundleSignatureURL: "https://signing.example/bundle.sig", TrustBundleSignatureKeyPath: keyPath})
	if crossOrigin.Passed {
		t.Fatalf("cross-origin remote Trust Bundle unexpectedly passed: %+v", crossOrigin)
	}
}

func TestRunRequiresManagedProductionShapeAndKMS(t *testing.T) {
	failed := Run(Options{AuthMode: "oidc", TLSCertPath: "cert", TLSKeyPath: "key"})
	if failed.Passed {
		t.Fatalf("incomplete production backend shape unexpectedly passed: %+v", failed)
	}
	valid := Run(Options{
		AuthMode: "oidc", TLSCertPath: "cert", TLSKeyPath: "key",
		StorageBackend: "postgres", ArtifactBackend: "s3", WorkflowInputStorage: "file",
		KMSURL: "https://kms.example", ArtifactKMSKeyID: "artifact-key", WorkflowKMSKeyID: "workflow-key",
		OutboxEndpoint: "tls://nats.example:4222",
	})
	// The intentionally fake certificate paths still make the TLS check fail;
	// this assertion verifies the new managed-backend checks are independently
	// present without weakening certificate validation.
	var sawKMS, sawEventing bool
	for _, check := range valid.Checks {
		sawKMS = sawKMS || check.ID == "production-kms" && check.Status == "passed"
		sawEventing = sawEventing || check.ID == "production-eventing" && check.Status == "passed"
	}
	if !sawKMS || !sawEventing {
		t.Fatalf("production checks missing: %+v", valid.Checks)
	}
}

func TestRunRejectsInsecureProductionKMS(t *testing.T) {
	report := Run(Options{
		AuthMode: "oidc", StorageBackend: "postgres", ArtifactBackend: "s3", WorkflowInputStorage: "file",
		KMSURL: "http://kms.example", ArtifactKMSKeyID: "artifact-key", WorkflowKMSKeyID: "workflow-key",
		OutboxEndpoint: "tls://nats.example:4222",
	})
	for _, check := range report.Checks {
		if check.ID == "production-kms" && check.Status == "failed" {
			return
		}
	}
	t.Fatalf("missing insecure KMS failure: %+v", report.Checks)
}
