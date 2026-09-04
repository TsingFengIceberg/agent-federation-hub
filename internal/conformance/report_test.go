package conformance

import (
	"strings"
	"testing"
)

func TestSummarizeKeepsPartialEvidenceExplicit(t *testing.T) {
	matrix := Matrix{SchemaVersion: 1, GoSDKModule: "github.com/a2aproject/a2a-go/v2", ProtocolVersion: "1.0", ProtocolSource: strings.Repeat("a", 40), GoSDKVersion: "v2.5.0", TCKCommit: strings.Repeat("b", 40), TCKProtocolCommit: strings.Repeat("c", 40), Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "accepted-with-waivers", MustPassed: 3, MustSkipped: 1, MustNotTested: 2}}}
	matrix.Profiles[0].TransportPassed = 1
	report, err := Summarize(matrix, WaiverDocument{Status: "accepted-with-waivers", Waivers: []Waiver{{ID: "AUTH", Scope: "authentication", Reason: "fixture", Evidence: "docs"}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompleteConformance || len(report.Reasons) != 4 || report.Profiles[0].Complete {
		t.Fatalf("report=%+v", report)
	}
}

func TestSummarizeCanMarkCompleteOnlyWithoutGapsOrWaivers(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	report, err := Summarize(Matrix{SchemaVersion: 1, GoSDKModule: "github.com/a2aproject/a2a-go/v2", ProtocolVersion: "1.0", ProtocolSource: commit, GoSDKVersion: "v2.5.0", TCKCommit: commit, TCKProtocolCommit: commit, Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "complete", MustPassed: 10, TransportPassed: 1}}}, WaiverDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.CompleteConformance || len(report.Reasons) != 0 || !report.Profiles[0].Complete {
		t.Fatalf("report=%+v", report)
	}
}

func TestSummarizeRejectsPinMismatch(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	_, err := Summarize(Matrix{SchemaVersion: 1, GoSDKModule: "github.com/a2aproject/a2a-go/v2", ProtocolVersion: "1.0", ProtocolSource: commit, GoSDKVersion: "v2.5.0", TCKCommit: commit, TCKProtocolCommit: commit, Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "partial"}}}, WaiverDocument{TCKCommit: "abcdefabcdefabcdefabcdefabcdefabcdefabcd"})
	if err == nil {
		t.Fatal("pin mismatch unexpectedly accepted")
	}
}

func TestMatrixValidationRejectsContradictoryEvidence(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	base := Matrix{
		SchemaVersion: 1, GoSDKModule: "github.com/a2aproject/a2a-go/v2",
		ProtocolVersion: "1.0", ProtocolSource: commit, GoSDKVersion: "v2.5.0",
		TCKCommit: commit, TCKProtocolCommit: commit,
		Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "verified-local-with-skips", MustPassed: 1, TransportPassed: 1}},
	}
	base.Profiles[0].MustFailed = 1
	if err := base.Validate(); err == nil {
		t.Fatal("failed MUST evidence was accepted")
	}
	base.Profiles[0].MustFailed = 0
	base.Profiles = append(base.Profiles, MatrixEntry{Name: "duplicate", Binding: "JSONRPC", Stream: "SSE", Status: "verified-local-with-skips", MustPassed: 1, TransportPassed: 1})
	if err := base.Validate(); err == nil {
		t.Fatal("duplicate Binding evidence was accepted")
	}
}

func TestWaiverValidationRequiresAttribution(t *testing.T) {
	if err := (WaiverDocument{Status: "aligned-v1.0.0", Waivers: []Waiver{{ID: "AUTH"}}}).Validate(); err == nil {
		t.Fatal("incomplete waiver was accepted")
	}
}

func TestMatrixValidationRejectsUnsupportedBindingAndStream(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	base := Matrix{
		SchemaVersion: 1, ProtocolVersion: "1.0", ProtocolSource: commit,
		GoSDKModule: "github.com/a2aproject/a2a-go/v2", GoSDKVersion: "v2.5.0",
		TCKCommit: commit, TCKProtocolCommit: commit,
		Profiles: []MatrixEntry{{Name: "profile", Binding: "JSONRPC", Stream: "SSE", Status: StatusPartial, MustPassed: 1, TransportPassed: 1}},
	}
	base.Profiles[0].Binding = "WEBSOCKET"
	if err := base.Validate(); err == nil {
		t.Fatal("unsupported Binding was accepted")
	}
	base.Profiles[0].Binding = "GRPC"
	base.Profiles[0].Stream = "SSE"
	if err := base.Validate(); err == nil {
		t.Fatal("invalid gRPC stream transport was accepted")
	}
}

func TestMatrixValidationRejectsUnknownStatusAndEmptyEvidence(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	base := Matrix{
		SchemaVersion: 1, ProtocolVersion: "1.0", ProtocolSource: commit,
		GoSDKModule: "github.com/a2aproject/a2a-go/v2", GoSDKVersion: "v2.5.0",
		TCKCommit: commit, TCKProtocolCommit: commit,
		Profiles: []MatrixEntry{{Name: "profile", Binding: "JSONRPC", Stream: "SSE", Status: "unknown", MustPassed: 1, TransportPassed: 1}},
	}
	if err := base.Validate(); err == nil {
		t.Fatal("unknown profile status was accepted")
	}
	base.Profiles[0].Status = StatusPartial
	base.Profiles[0].MustPassed = 0
	base.Profiles[0].TransportPassed = 0
	if err := base.Validate(); err == nil {
		t.Fatal("empty evidence counts were accepted")
	}
}

func TestMatrixValidationRejectsCompleteProfileWithGaps(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	matrix := Matrix{
		SchemaVersion: 1, ProtocolVersion: "1.0", ProtocolSource: commit,
		GoSDKModule: "github.com/a2aproject/a2a-go/v2", GoSDKVersion: "v2.5.0",
		TCKCommit: commit, TCKProtocolCommit: commit,
		Profiles: []MatrixEntry{{Name: "profile", Binding: "JSONRPC", Stream: "SSE", Status: StatusComplete, MustPassed: 1, MustSkipped: 1, TransportPassed: 1}},
	}
	if err := matrix.Validate(); err == nil {
		t.Fatal("complete profile with skipped evidence was accepted")
	}
}
