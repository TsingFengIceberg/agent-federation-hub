package conformance_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type profile struct {
	ProtocolVersion            string `json:"protocolVersion"`
	ProtocolSourceCommit       string `json:"protocolSourceCommit"`
	Binding                    string `json:"binding"`
	StreamTransport            string `json:"streamTransport"`
	GoSDKModule                string `json:"goSDKModule"`
	GoSDKVersion               string `json:"goSDKVersion"`
	EvaluatedTCKCommit         string `json:"evaluatedTCKCommit"`
	EvaluatedTCKProtocolCommit string `json:"evaluatedTCKProtocolCommit"`
	RepositoryContractStatus   string `json:"repositoryContractStatus"`
	ExternalTCKStatus          string `json:"externalTCKStatus"`
}

func TestSelectedA2AProfilePinsStayExplicit(t *testing.T) {
	payload, err := os.ReadFile("a2a-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var selected profile
	if err := json.Unmarshal(payload, &selected); err != nil {
		t.Fatal(err)
	}
	if selected.ProtocolVersion != "1.0" || selected.Binding != "JSONRPC" || selected.StreamTransport != "SSE" {
		t.Fatalf("unexpected selected wire profile: %+v", selected)
	}
	if len(selected.ProtocolSourceCommit) != 40 || len(selected.EvaluatedTCKCommit) != 40 || len(selected.EvaluatedTCKProtocolCommit) != 40 {
		t.Fatal("protocol and TCK sources must be full commit IDs")
	}
	if selected.RepositoryContractStatus != "verified-local" {
		t.Fatal("repository contract status must be explicit")
	}
	if selected.ExternalTCKStatus != "unresolved-revision-skew" {
		t.Fatal("external TCK must not be represented as passing while revision skew remains")
	}
	goMod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), selected.GoSDKModule+" "+selected.GoSDKVersion) {
		t.Fatalf("go.mod does not match selected SDK %s %s", selected.GoSDKModule, selected.GoSDKVersion)
	}
}
