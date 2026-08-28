package agentconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `schema_version: 1
defaults:
  protocol:
    kind: a2a
    profiles:
      - protocol_version: "1.0"
        binding: JSONRPC
        stream_transport: SSE
  discovery:
    refresh_interval_seconds: 300
    max_card_bytes: 262144
    require_https: true
    allow_private_urls: false
  execution:
    remote_timeout_seconds: 30
    task_timeout_seconds: 1800
    max_retries: 2
    cancel_grace_seconds: 10
    reconnect: true
  limits:
    max_concurrent_tasks: 16
    max_artifact_bytes: 10485760
agents:
  - id: agent-a
    tenant_id: tenant-a
    enabled: true
    card_url: https://agent.example/.well-known/agent-card.json
    discovery:
      required_capabilities:
        streaming: true
      required_skills: [research]
    credential_env:
      oauth: REMOTE_AGENT_TOKEN
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent_config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileAndRegistrationPolicy(t *testing.T) {
	file, err := LoadFile(writeConfig(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.EnabledAgents()) != 1 || file.EnabledAgents()[0].ID != "agent-a" {
		t.Fatalf("enabled agents=%+v", file.EnabledAgents())
	}
	policy := file.EnabledAgents()[0].RegistrationPolicy(file.Defaults)
	if policy.RequiredProtocolVersion != "1.0" || policy.RequiredProtocolBinding != "JSONRPC" ||
		!policy.RequireStreaming || len(policy.RequiredSkills) != 1 || policy.RequiredSkills[0] != "research" {
		t.Fatalf("policy=%+v", policy)
	}
}

func TestLoadFileRejectsDuplicateIDsAndUnsafeURL(t *testing.T) {
	duplicate := validYAML + "\n  - id: agent-a\n    tenant_id: tenant-b\n    enabled: false\n    card_url: https://other.example/card\n"
	if _, err := LoadFile(writeConfig(t, duplicate)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate error=%v", err)
	}
	unsafe := strings.Replace(validYAML, "https://agent.example", "http://agent.example", 1)
	if _, err := LoadFile(writeConfig(t, unsafe)); err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("unsafe URL error=%v", err)
	}
}

func TestLoadFileRejectsUnknownFieldsAndInvalidCredentials(t *testing.T) {
	unknown := validYAML + "unexpected: true\n"
	if _, err := LoadFile(writeConfig(t, unknown)); err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("unknown field error=%v", err)
	}
	invalidCredential := strings.Replace(validYAML, "REMOTE_AGENT_TOKEN", "not-a-valid-reference", 1)
	if _, err := LoadFile(writeConfig(t, invalidCredential)); err == nil || !strings.Contains(err.Error(), "valid environment variable") {
		t.Fatalf("credential error=%v", err)
	}
}

func TestRepositoryExamplesRemainValid(t *testing.T) {
	if _, err := LoadFile("../../agent_config.example.yaml"); err != nil {
		t.Fatalf("load agent_config.example.yaml: %v", err)
	}
}
