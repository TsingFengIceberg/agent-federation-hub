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
	trustBundleURL := flag.String("trust-bundle-url", "", "optional HTTPS signed Trust Bundle distribution URL (shape check only)")
	trustBundleSignatureURL := flag.String("trust-bundle-signature-url", "", "HTTPS detached signature URL for trust-bundle-url")
	trustBundleSignature := flag.String("trust-bundle-signature", "", "optional detached Trust Bundle signature")
	trustBundleSignatureKey := flag.String("trust-bundle-signature-key", "", "optional PEM public key for Trust Bundle signature")
	accessPolicy := flag.String("access-policy", "", "optional access policy JSON")
	authMode := flag.String("auth-mode", "development", "Hub authentication mode")
	tlsCert := flag.String("tls-cert-file", "", "PEM TLS server certificate")
	tlsKey := flag.String("tls-key-file", "", "PEM TLS server private key")
	profileMatrix := flag.String("profile-matrix", "tests/conformance/profile-matrix.json", "repository-owned A2A Profile matrix")
	storageBackend := flag.String("storage-backend", "journal", "Hub storage backend: journal or postgres")
	artifactBackend := flag.String("artifact-backend", "filesystem", "Artifact backend: filesystem or s3")
	workflowInputStorage := flag.String("workflow-input-storage", "memory", "Workflow input backend: memory or file")
	kmsURL := flag.String("kms-url", "", "optional HTTPS KMS/data-key endpoint")
	artifactKMSKeyID := flag.String("artifact-kms-key-id", "", "KMS key ID for Artifact encryption")
	workflowKMSKeyID := flag.String("workflow-kms-key-id", "", "KMS key ID for Workflow input encryption")
	outboxEndpoint := flag.String("outbox-endpoint", "", "durable Outbox endpoint (NATS, CloudEvents, or HTTPS)")
	output := flag.String("output", "text", "output format: text or json")
	flag.Parse()

	report := preflight.Run(preflight.Options{
		AgentConfigPath: *agentConfig, TrustBundlePath: *trustBundle,
		TrustBundleURL: *trustBundleURL, TrustBundleSignatureURL: *trustBundleSignatureURL,
		TrustBundleSignaturePath: *trustBundleSignature, TrustBundleSignatureKeyPath: *trustBundleSignatureKey,
		AccessPolicyPath: *accessPolicy, AuthMode: *authMode,
		TLSCertPath: *tlsCert, TLSKeyPath: *tlsKey, ProfileMatrix: *profileMatrix,
		StorageBackend: *storageBackend, ArtifactBackend: *artifactBackend,
		WorkflowInputStorage: *workflowInputStorage, KMSURL: *kmsURL,
		ArtifactKMSKeyID: *artifactKMSKeyID, WorkflowKMSKeyID: *workflowKMSKeyID,
		OutboxEndpoint: *outboxEndpoint,
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
