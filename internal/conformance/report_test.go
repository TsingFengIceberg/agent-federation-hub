package conformance

import (
	"strings"
	"testing"
)

func TestSummarizeKeepsPartialEvidenceExplicit(t *testing.T) {
	matrix := Matrix{ProtocolVersion: "1.0", ProtocolSource: strings.Repeat("a", 40), GoSDKVersion: "v2.5.0", TCKCommit: strings.Repeat("b", 40), TCKProtocolCommit: strings.Repeat("c", 40), Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "accepted-with-waivers", MustPassed: 3, MustSkipped: 1, MustNotTested: 2}}}
	report, err := Summarize(matrix, WaiverDocument{Waivers: []Waiver{{ID: "AUTH", Scope: "authentication", Reason: "fixture", Evidence: "docs"}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompleteConformance || len(report.Reasons) != 4 || report.Profiles[0].Complete {
		t.Fatalf("report=%+v", report)
	}
}

func TestSummarizeCanMarkCompleteOnlyWithoutGapsOrWaivers(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	report, err := Summarize(Matrix{ProtocolVersion: "1.0", ProtocolSource: commit, GoSDKVersion: "v2.5.0", TCKCommit: commit, TCKProtocolCommit: commit, Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "complete", MustPassed: 10}}}, WaiverDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.CompleteConformance || len(report.Reasons) != 0 || !report.Profiles[0].Complete {
		t.Fatalf("report=%+v", report)
	}
}

func TestSummarizeRejectsPinMismatch(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	_, err := Summarize(Matrix{ProtocolVersion: "1.0", ProtocolSource: commit, GoSDKVersion: "v2.5.0", TCKCommit: commit, TCKProtocolCommit: commit, Profiles: []MatrixEntry{{Name: "jsonrpc", Binding: "JSONRPC", Stream: "SSE", Status: "partial"}}}, WaiverDocument{TCKCommit: "abcdefabcdefabcdefabcdefabcdefabcdefabcd"})
	if err == nil {
		t.Fatal("pin mismatch unexpectedly accepted")
	}
}
