package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

type hubContract struct {
	ContractVersion  string            `json:"contractVersion"`
	Status           string            `json:"status"`
	Product          string            `json:"product"`
	DefaultProfile   contractProfile   `json:"defaultProfile"`
	OptInProfiles    []contractProfile `json:"optInProfiles"`
	ProviderBoundary string            `json:"providerBoundary"`
	HubOwns          []string          `json:"hubOwns"`
	ProviderOwns     []string          `json:"providerOwns"`
	TaskStates       []string          `json:"taskStates"`
	TerminalStates   []string          `json:"terminalTaskStates"`
	DeliveryStates   []string          `json:"deliveryStates"`
	AuthModes        []string          `json:"authModes"`
	AAMP             string            `json:"aampRole"`
	Operations       []contractOp      `json:"managementOperations"`
}

type contractProfile struct {
	ProtocolVersion string `json:"protocolVersion"`
	Binding         string `json:"binding"`
	StreamTransport string `json:"streamTransport"`
}

type contractOp struct {
	Action string `json:"action"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Scope  string `json:"scope"`
}

func TestHubProductContractIsExplicitAndStable(t *testing.T) {
	payload, err := os.ReadFile("hub-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var contract hubContract
	if err := decoder.Decode(&contract); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("contract contains trailing JSON: %v", err)
	}
	if contract.Product != "agent-federation-hub" || contract.ContractVersion != "1.0" || contract.Status != "accepted-initial" {
		t.Fatalf("unexpected contract metadata: %+v", contract)
	}
	if contract.ProviderBoundary != "opaque" || contract.AAMP != "asynchronous-mailbox-adapter" {
		t.Fatalf("provider/AAMP boundaries are not explicit: %+v", contract)
	}
	if contract.DefaultProfile != (contractProfile{ProtocolVersion: "1.0", Binding: "JSONRPC", StreamTransport: "SSE"}) {
		t.Fatalf("unexpected default profile: %+v", contract.DefaultProfile)
	}
	if len(contract.OptInProfiles) != 2 || contract.OptInProfiles[0].Binding != "HTTP+JSON" || contract.OptInProfiles[1].Binding != "GRPC" {
		t.Fatalf("unexpected opt-in profiles: %+v", contract.OptInProfiles)
	}
	for _, state := range []string{"SUBMITTED", "WORKING", "INPUT_REQUIRED", "AUTH_REQUIRED", "COMPLETED", "FAILED", "CANCELED", "REJECTED", "UNKNOWN"} {
		if !contains(contract.TaskStates, state) {
			t.Fatalf("task state %q is missing", state)
		}
	}
	for _, state := range []string{"COMPLETED", "FAILED", "CANCELED", "REJECTED"} {
		if !contains(contract.TerminalStates, state) {
			t.Fatalf("terminal state %q is missing", state)
		}
	}
	for _, state := range []string{"PENDING", "ACKNOWLEDGED", "AMBIGUOUS"} {
		if !contains(contract.DeliveryStates, state) {
			t.Fatalf("delivery state %q is missing", state)
		}
	}
	for _, mode := range []string{"oidc", "mtls", "oidc-or-mtls", "jwt-static", "development"} {
		if !contains(contract.AuthModes, mode) {
			t.Fatalf("auth mode %q is missing", mode)
		}
	}
	if len(contract.HubOwns) < 5 || len(contract.ProviderOwns) < 5 || len(contract.Operations) < 9 {
		t.Fatalf("contract omits ownership or management operations: %+v", contract)
	}
	expected := map[string]contractOp{
		"agent.register /v1/agents":                              {Action: "agent.register", Method: "POST", Path: "/v1/agents", Scope: "agents:write"},
		"agent.list /v1/agents":                                  {Action: "agent.list", Method: "GET", Path: "/v1/agents", Scope: "agents:read"},
		"agent.refresh /v1/agents/{agentID}/refresh":             {Action: "agent.refresh", Method: "POST", Path: "/v1/agents/{agentID}/refresh", Scope: "agents:write"},
		"task.submit /v1/tasks":                                  {Action: "task.submit", Method: "POST", Path: "/v1/tasks", Scope: "tasks:submit"},
		"task.continue /v1/tasks/{taskID}/messages":              {Action: "task.continue", Method: "POST", Path: "/v1/tasks/{taskID}/messages", Scope: "tasks:continue"},
		"task.read /v1/tasks/{taskID}":                           {Action: "task.read", Method: "GET", Path: "/v1/tasks/{taskID}", Scope: "tasks:read"},
		"task.events /v1/tasks/{taskID}/events":                  {Action: "task.events", Method: "GET", Path: "/v1/tasks/{taskID}/events", Scope: "tasks:read"},
		"task.cancel /v1/tasks/{taskID}/cancel":                  {Action: "task.cancel", Method: "POST", Path: "/v1/tasks/{taskID}/cancel", Scope: "tasks:cancel"},
		"task.reconcile /v1/tasks/{taskID}/reconcile":            {Action: "task.reconcile", Method: "POST", Path: "/v1/tasks/{taskID}/reconcile", Scope: "tasks:reconcile"},
		"workflow.create /v1/workflows":                          {Action: "workflow.create", Method: "POST", Path: "/v1/workflows", Scope: "workflows:write"},
		"workflow.list /v1/workflows":                            {Action: "workflow.list", Method: "GET", Path: "/v1/workflows", Scope: "workflows:read"},
		"workflow.read /v1/workflows/{workflowID}":               {Action: "workflow.read", Method: "GET", Path: "/v1/workflows/{workflowID}", Scope: "workflows:read"},
		"workflow.control /v1/workflows/{workflowID}/reconcile":  {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/reconcile", Scope: "workflows:control"},
		"workflow.control /v1/workflows/{workflowID}/continue":   {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/continue", Scope: "workflows:control"},
		"workflow.control /v1/workflows/{workflowID}/pause":      {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/pause", Scope: "workflows:control"},
		"workflow.control /v1/workflows/{workflowID}/resume":     {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/resume", Scope: "workflows:control"},
		"workflow.control /v1/workflows/{workflowID}/cancel":     {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/cancel", Scope: "workflows:control"},
		"workflow.control /v1/workflows/{workflowID}/compensate": {Action: "workflow.control", Method: "POST", Path: "/v1/workflows/{workflowID}/compensate", Scope: "workflows:control"},
		"worker.read /v1/workers":                                {Action: "worker.read", Method: "GET", Path: "/v1/workers", Scope: "workers:read"},
		"worker.control /v1/workers/pause":                       {Action: "worker.control", Method: "POST", Path: "/v1/workers/pause", Scope: "workers:control"},
		"worker.control /v1/workers/resume":                      {Action: "worker.control", Method: "POST", Path: "/v1/workers/resume", Scope: "workers:control"},
		"worker.control /v1/workers/drain":                       {Action: "worker.control", Method: "POST", Path: "/v1/workers/drain", Scope: "workers:control"},
		"artifact.read /v1/artifacts/{artifactID}":               {Action: "artifact.read", Method: "GET", Path: "/v1/artifacts/{artifactID}", Scope: "artifacts:read"},
	}
	if len(contract.Operations) != len(expected) {
		t.Fatalf("operation count=%d expected=%d", len(contract.Operations), len(expected))
	}
	for _, operation := range contract.Operations {
		if operation.Action == "" || operation.Method == "" || operation.Path == "" || operation.Scope == "" {
			t.Fatalf("incomplete management operation: %+v", operation)
		}
		key := operation.Action + " " + operation.Path
		want, ok := expected[key]
		if !ok || operation != want {
			t.Fatalf("operation does not match Hub handler contract: %+v", operation)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatalf("contract is missing operations: %+v", expected)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
