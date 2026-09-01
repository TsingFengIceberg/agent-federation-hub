// Command a2a-compat-report summarizes the pinned A2A evidence files. It does
// not run a TCK; use tests/conformance/run-matrix.sh for that operation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/conformance"
)

func main() {
	matrixPath := flag.String("matrix", "tests/conformance/profile-matrix.json", "A2A Profile matrix JSON")
	waiverPath := flag.String("waivers", "tests/conformance/tck-waivers.json", "A2A waiver JSON")
	output := flag.String("output", "text", "output format: text or json")
	requireComplete := flag.Bool("require-complete", false, "exit non-zero when skipped, not-tested, failed, or waived evidence remains")
	flag.Parse()

	matrix, err := conformance.LoadMatrix(*matrixPath)
	if err != nil {
		fail(*output, err)
	}
	waivers, err := conformance.LoadWaivers(*waiverPath)
	if err != nil {
		fail(*output, err)
	}
	report, err := conformance.Summarize(matrix, waivers)
	if err != nil {
		fail(*output, err)
	}
	writeReport(os.Stdout, *output, report)
	if *requireComplete && !report.CompleteConformance {
		os.Exit(1)
	}
}

func writeReport(writer io.Writer, format string, report conformance.Report) {
	if format == "json" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_, _ = fmt.Fprintln(writer, string(encoded))
		}
		return
	}
	fmt.Fprintf(writer, "Evidence: %s\nProtocol: %s (%s)\nTCK: %s\nComplete conformance: %t\n", report.EvidenceStatus, report.ProtocolVersion, report.ProtocolSource, report.TCKCommit, report.CompleteConformance)
	for _, profile := range report.Profiles {
		fmt.Fprintf(writer, "- %s %s/%s: %s; MUST passed=%d skipped=%d not-tested=%d failed=%d\n", profile.Name, profile.Binding, profile.Stream, profile.Status, profile.MustPassed, profile.MustSkipped, profile.MustNotTested, profile.MustFailed)
	}
	if len(report.Waivers) > 0 {
		fmt.Fprintln(writer, "Waivers:")
		for _, waiver := range report.Waivers {
			fmt.Fprintf(writer, "- %s (%s)\n", waiver.ID, waiver.Scope)
		}
	}
	for _, reason := range report.Reasons {
		fmt.Fprintf(writer, "Gap: %s\n", reason)
	}
}

func fail(format string, err error) {
	if format == "json" {
		encoded, marshalErr := json.Marshal(map[string]any{"error": err.Error(), "evidenceStatus": "invalid-input"})
		if marshalErr == nil {
			fmt.Println(string(encoded))
		}
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
	}
	os.Exit(2)
}
