package conformance

import (
	"encoding/json"
	"os"
	"testing"
)

type profileMatrix struct {
	ProtocolVersion   string        `json:"protocolVersion"`
	ProtocolSource    string        `json:"protocolSourceCommit"`
	GoSDKVersion      string        `json:"goSDKVersion"`
	GoSDKSource       string        `json:"goSDKSourceCommit"`
	TCKCommit         string        `json:"tckCommit"`
	TCKProtocolCommit string        `json:"tckProtocolCommit"`
	Profiles          []matrixEntry `json:"profiles"`
}

type matrixEntry struct {
	Name        string `json:"name"`
	Binding     string `json:"binding"`
	Stream      string `json:"streamTransport"`
	SUTBinding  string `json:"sutBindingFlag"`
	Status      string `json:"status"`
	MustPassed  int    `json:"tckMustPassed"`
	MustSkipped int    `json:"tckMustSkipped"`
	MustFailed  int    `json:"tckMustFailed"`
}

func TestProfileMatrixPinsAndClaims(t *testing.T) {
	data, err := os.ReadFile("profile-matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	var matrix profileMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"protocol version": matrix.ProtocolVersion,
		"protocol source":  matrix.ProtocolSource,
		"Go SDK version":   matrix.GoSDKVersion,
		"Go SDK source":    matrix.GoSDKSource,
		"TCK commit":       matrix.TCKCommit,
		"TCK protocol":     matrix.TCKProtocolCommit,
	} {
		if value == "" {
			t.Fatalf("%s pin is required", name)
		}
	}
	if len(matrix.Profiles) != 3 {
		t.Fatalf("profile matrix entries=%d, want 3", len(matrix.Profiles))
	}
	seen := map[string]bool{}
	for _, profile := range matrix.Profiles {
		if profile.Name == "" || profile.Binding == "" || profile.Stream == "" || profile.SUTBinding == "" {
			t.Fatalf("incomplete profile entry: %+v", profile)
		}
		if seen[profile.Binding] {
			t.Fatalf("duplicate binding %q", profile.Binding)
		}
		seen[profile.Binding] = true
		if profile.MustFailed != 0 {
			t.Fatalf("profile %q records failed MUST requirements: %d", profile.Name, profile.MustFailed)
		}
		if profile.Binding == "GRPC" && profile.Status != "not-implemented" {
			t.Fatalf("gRPC profile must remain explicitly not implemented")
		}
		if profile.Binding == "HTTP+JSON" && profile.Status == "accepted" {
			t.Fatalf("HTTP+JSON cannot be accepted without the current product gate")
		}
	}
	for _, binding := range []string{"JSONRPC", "HTTP+JSON", "GRPC"} {
		if !seen[binding] {
			t.Fatalf("profile matrix missing %s", binding)
		}
	}
}
