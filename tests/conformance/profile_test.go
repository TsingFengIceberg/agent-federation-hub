package conformance_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type profile struct {
	ProfileName                string   `json:"profileName"`
	ProtocolVersion            string   `json:"protocolVersion"`
	ProtocolSourceCommit       string   `json:"protocolSourceCommit"`
	Binding                    string   `json:"binding"`
	StreamTransport            string   `json:"streamTransport"`
	GoSDKModule                string   `json:"goSDKModule"`
	GoSDKVersion               string   `json:"goSDKVersion"`
	EvaluatedTCKCommit         string   `json:"evaluatedTCKCommit"`
	EvaluatedTCKProtocolCommit string   `json:"evaluatedTCKProtocolCommit"`
	RepositoryContractStatus   string   `json:"repositoryContractStatus"`
	ExternalTCKStatus          string   `json:"externalTCKStatus"`
	OwnedSUT                   string   `json:"ownedSUT"`
	OwnedSUTProfile            string   `json:"ownedSUTProfile"`
	OwnedSUTStatus             string   `json:"ownedSUTStatus"`
	WaiverFile                 string   `json:"waiverFile"`
	Runner                     string   `json:"runner"`
	SupportedBindings          []string `json:"supportedBindings"`
	SupportedStreamTransports  []string `json:"supportedStreamTransports"`
	BindingSelection           string   `json:"bindingSelection"`
}

type waiverDocument struct {
	Status            string `json:"status"`
	TCKCommit         string `json:"tckCommit"`
	TCKProtocolCommit string `json:"tckProtocolCommit"`
	Waivers           []struct {
		ID       string `json:"id"`
		Scope    string `json:"scope"`
		Reason   string `json:"reason"`
		Evidence string `json:"evidence"`
	} `json:"waivers"`
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
	if selected.ProfileName != "a2a-v1-jsonrpc-sse" || selected.BindingSelection != "explicit" ||
		len(selected.SupportedBindings) != 1 || selected.SupportedBindings[0] != "JSONRPC" ||
		len(selected.SupportedStreamTransports) != 1 || selected.SupportedStreamTransports[0] != "SSE" {
		t.Fatalf("profile coverage is not explicit: %+v", selected)
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
	if selected.OwnedSUT != "cmd/a2a-tck-sut" || selected.OwnedSUTProfile != "JSONRPC+SSE" || selected.OwnedSUTStatus != "implemented" {
		t.Fatalf("owned SUT metadata is incomplete: %+v", selected)
	}
	if selected.WaiverFile == "" || selected.Runner == "" {
		t.Fatal("TCK waiver file and runner must be explicit")
	}
	waiverPayload, err := os.ReadFile(selected.WaiverFile)
	if err != nil {
		t.Fatal(err)
	}
	var waivers waiverDocument
	if err := json.Unmarshal(waiverPayload, &waivers); err != nil {
		t.Fatal(err)
	}
	if waivers.Status != selected.ExternalTCKStatus || waivers.TCKCommit != selected.EvaluatedTCKCommit || len(waivers.TCKProtocolCommit) != 40 {
		t.Fatalf("waiver metadata does not match profile: %+v", waivers)
	}
	if len(waivers.Waivers) == 0 {
		t.Fatal("at least one explicit TCK waiver is required while alignment is unresolved")
	}
	for _, waiver := range waivers.Waivers {
		if waiver.ID == "" || waiver.Scope == "" || waiver.Reason == "" || waiver.Evidence == "" {
			t.Fatalf("incomplete TCK waiver: %+v", waiver)
		}
	}
	goMod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), selected.GoSDKModule+" "+selected.GoSDKVersion) {
		t.Fatalf("go.mod does not match selected SDK %s %s", selected.GoSDKModule, selected.GoSDKVersion)
	}
}
