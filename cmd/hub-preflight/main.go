// Command hub-preflight performs local, non-destructive checks before a Hub
// process is started. It never calls external trust, registry, or storage
// services and therefore cannot establish production qualification.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/preflight"
)

func main() {
	agentConfig := flag.String("agent-config", "agent_config.yaml", "Agent registration YAML; missing file is allowed")
	trustBundle := flag.String("trust-bundle", "", "optional Trust Bundle JSON")
	accessPolicy := flag.String("access-policy", "", "optional access policy JSON")
	authMode := flag.String("auth-mode", "development", "Hub authentication mode")
	tlsCert := flag.String("tls-cert-file", "", "PEM TLS server certificate")
	tlsKey := flag.String("tls-key-file", "", "PEM TLS server private key")
	profileMatrix := flag.String("profile-matrix", "tests/conformance/profile-matrix.json", "repository-owned A2A Profile matrix")
	output := flag.String("output", "text", "output format: text or json")
	flag.Parse()

	report := preflight.Run(preflight.Options{
		AgentConfigPath: *agentConfig, TrustBundlePath: *trustBundle,
		AccessPolicyPath: *accessPolicy, AuthMode: *authMode,
		TLSCertPath: *tlsCert, TLSKeyPath: *tlsKey, ProfileMatrix: *profileMatrix,
	})
	writeReport(os.Stdout, *output, report)
	if !report.Passed {
		os.Exit(1)
	}
}

func writeReport(writer io.Writer, format string, report preflight.Report) {
	if format == "json" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_, _ = fmt.Fprintln(writer, string(encoded))
		}
		return
	}
	fmt.Fprintf(writer, "Evidence: %s\nResult: %s\n", report.EvidenceStatus, map[bool]string{true: "PASSED", false: "FAILED"}[report.Passed])
	for _, check := range report.Checks {
		fmt.Fprintf(writer, "[%s] %s: %s", strings.ToUpper(check.Status), check.ID, check.Message)
		if check.Evidence != "" {
			fmt.Fprintf(writer, " (%s)", check.Evidence)
		}
		fmt.Fprintln(writer)
	}
}
