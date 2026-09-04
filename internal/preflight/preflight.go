// Package preflight provides local, non-destructive deployment checks. It
// deliberately reports configuration evidence separately from production
// qualification, which requires external infrastructure and operational data.
package preflight

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/agentconfig"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/conformance"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
)

const ReportVersion = 1

type Options struct {
	AgentConfigPath             string
	TrustBundlePath             string
	TrustBundleURL              string
	TrustBundleSignatureURL     string
	TrustBundleSignaturePath    string
	TrustBundleSignatureKeyPath string
	AccessPolicyPath            string
	AuthMode                    string
	TLSCertPath                 string
	TLSKeyPath                  string
	ProfileMatrix               string
	StorageBackend              string
	ArtifactBackend             string
	WorkflowInputStorage        string
	KMSURL                      string
	ArtifactKMSKeyID            string
	WorkflowKMSKeyID            string
	OutboxEndpoint              string
	Now                         time.Time
}

type Check struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Evidence string `json:"evidence,omitempty"`
}

type Report struct {
	Version        int       `json:"version"`
	EvidenceStatus string    `json:"evidenceStatus"`
	GeneratedAt    time.Time `json:"generatedAt"`
	Checks         []Check   `json:"checks"`
	Passed         bool      `json:"passed"`
}

func Run(options Options) Report {
	now := options.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := Report{Version: ReportVersion, EvidenceStatus: "local-configuration", GeneratedAt: now.UTC(), Checks: []Check{}}
	report.Checks = append(report.Checks, checkAgentConfig(options.AgentConfigPath)...)
	report.Checks = append(report.Checks, checkTrustBundle(options.TrustBundlePath, options.TrustBundleURL, options.TrustBundleSignatureURL, options.TrustBundleSignaturePath, options.TrustBundleSignatureKeyPath, now)...)
	report.Checks = append(report.Checks, checkAccessPolicy(options.AccessPolicyPath)...)
	report.Checks = append(report.Checks, checkTLS(options.AuthMode, options.TLSCertPath, options.TLSKeyPath)...)
	report.Checks = append(report.Checks, checkProductionBackends(options)...)
	report.Checks = append(report.Checks, checkProfileMatrix(options.ProfileMatrix)...)
	report.Passed = true
	for _, check := range report.Checks {
		if check.Status == "failed" {
			report.Passed = false
			break
		}
	}
	return report
}

// checkProductionBackends validates the deployment shape required by the
// production authentication boundary. It deliberately performs no network
// calls: endpoint reachability and managed-service SLOs belong to the
// deployment qualification suite. Development profiles retain the smaller
// journal/memory/filesystem defaults.
func checkProductionBackends(options Options) []Check {
	if strings.TrimSpace(options.AuthMode) == "" || options.AuthMode == "development" {
		return []Check{{ID: "production-backends", Status: "skipped", Message: "development authentication does not require managed backends"}}
	}
	checks := make([]Check, 0, 2)
	if options.StorageBackend != "postgres" {
		checks = append(checks, Check{ID: "production-storage", Status: "failed", Message: "non-development authentication requires PostgreSQL storage"})
	}
	if options.ArtifactBackend != "s3" {
		checks = append(checks, Check{ID: "production-artifacts", Status: "failed", Message: "non-development authentication requires an S3-compatible Artifact backend"})
	}
	if options.WorkflowInputStorage != "postgres" {
		checks = append(checks, Check{ID: "production-workflow-input", Status: "failed", Message: "non-development authentication requires the multi-instance PostgreSQL Workflow input vault"})
	}
	if strings.TrimSpace(options.KMSURL) == "" || strings.TrimSpace(options.ArtifactKMSKeyID) == "" || strings.TrimSpace(options.WorkflowKMSKeyID) == "" {
		checks = append(checks, Check{ID: "production-kms", Status: "failed", Message: "non-development authentication requires an HTTPS KMS endpoint and Artifact/Workflow key IDs"})
	} else if parsed, err := url.Parse(strings.TrimSpace(options.KMSURL)); err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		checks = append(checks, Check{ID: "production-kms", Status: "failed", Message: "KMS endpoint must be an HTTPS URL without user, query, or fragment"})
	} else {
		checks = append(checks, Check{ID: "production-kms", Status: "passed", Message: "KMS endpoint and tenant key IDs are configured", Evidence: "local shape check; KMS/HSM availability remains external"})
	}
	if strings.TrimSpace(options.OutboxEndpoint) == "" {
		checks = append(checks, Check{ID: "production-eventing", Status: "failed", Message: "non-development authentication requires a durable Outbox endpoint"})
	} else {
		checks = append(checks, Check{ID: "production-eventing", Status: "passed", Message: "durable Outbox endpoint is configured", Evidence: "local shape check; broker HA/SLO remains external"})
	}
	if len(checks) == 0 {
		return []Check{{ID: "production-backends", Status: "passed", Message: "managed production backend shape is configured"}}
	}
	return checks
}

func checkAgentConfig(path string) []Check {
	if strings.TrimSpace(path) == "" {
		return []Check{{ID: "agent-config", Status: "skipped", Message: "Agent configuration path was not supplied"}}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []Check{{ID: "agent-config", Status: "skipped", Message: "Agent configuration file does not exist; no configured Agents will be loaded"}}
	}
	if _, err := agentconfig.LoadFile(path); err != nil {
		return []Check{{ID: "agent-config", Status: "failed", Message: err.Error()}}
	}
	return []Check{{ID: "agent-config", Status: "passed", Message: "Agent configuration schema and policies are valid", Evidence: "local parser"}}
}

func checkTrustBundle(path, bundleURL, signatureURL, signaturePath, signatureKeyPath string, now time.Time) []Check {
	if strings.TrimSpace(bundleURL) != "" {
		if strings.TrimSpace(path) != "" || strings.TrimSpace(signaturePath) != "" {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: "Trust Bundle URL cannot be combined with local Trust Bundle files"}}
		}
		if strings.TrimSpace(signatureURL) == "" || strings.TrimSpace(signatureKeyPath) == "" {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: "Trust Bundle URL requires a detached signature URL and public key"}}
		}
		policy := netpolicy.HTTPSOnlyPolicy()
		bundle, bundleErr := policy.ValidateBaseURL(strings.TrimSpace(bundleURL))
		if bundleErr != nil {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: fmt.Sprintf("Trust Bundle URL must be an HTTPS base URL without user, query, or fragment: %v", bundleErr)}}
		}
		signature, signatureErr := policy.ValidateBaseURL(strings.TrimSpace(signatureURL))
		if signatureErr != nil {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: fmt.Sprintf("Trust Bundle signature URL must be an HTTPS base URL without user, query, or fragment: %v", signatureErr)}}
		}
		if !netpolicy.SameOrigin(bundle, signature) {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: "Trust Bundle URL and signature URL must use the same HTTPS origin"}}
		}
		if _, err := os.Stat(signatureKeyPath); err != nil {
			return []Check{{ID: "trust-bundle-source", Status: "failed", Message: fmt.Sprintf("read Trust Bundle signing key: %v", err)}}
		}
		return []Check{{ID: "trust-bundle-source", Status: "passed", Message: "signed HTTPS Trust Bundle source has valid URL and key configuration", Evidence: "local shape check; network fetch and operator distribution remain external"}}
	}
	if strings.TrimSpace(signatureURL) != "" {
		return []Check{{ID: "trust-bundle-source", Status: "failed", Message: "Trust Bundle signature URL requires a Trust Bundle URL"}}
	}
	if (strings.TrimSpace(signaturePath) == "") != (strings.TrimSpace(signatureKeyPath) == "") {
		return []Check{{ID: "trust-bundle-signature", Status: "failed", Message: "Trust Bundle signature and public key paths must be supplied together"}}
	}
	if strings.TrimSpace(path) == "" {
		if signaturePath != "" || signatureKeyPath != "" {
			return []Check{{ID: "trust-bundle-signature", Status: "failed", Message: "Trust Bundle signature requires a Trust Bundle path"}}
		}
		return []Check{{ID: "trust-bundle", Status: "skipped", Message: "Trust Bundle path was not supplied"}}
	}
	var bundle hub.TrustBundle
	var err error
	if signaturePath != "" {
		manager, managerErr := hub.NewSignedTrustBundleManager(path, signaturePath, signatureKeyPath)
		if managerErr != nil {
			return []Check{{ID: "trust-bundle-signature", Status: "failed", Message: managerErr.Error()}}
		}
		bundle, _ = manager.Snapshot()
	} else {
		bundle, err = hub.LoadTrustBundleFile(path)
	}
	if err != nil {
		return []Check{{ID: "trust-bundle", Status: "failed", Message: err.Error()}}
	}
	if err := bundle.ValidateAt(now); err != nil {
		return []Check{{ID: "trust-bundle", Status: "failed", Message: err.Error()}}
	}
	evidence := "local parser and time-bound validation"
	if signaturePath != "" {
		evidence += "; detached signature verified"
	}
	return []Check{{ID: "trust-bundle", Status: "passed", Message: fmt.Sprintf("Trust Bundle generation %d is valid and active", bundle.Generation), Evidence: evidence}}
}

func checkAccessPolicy(path string) []Check {
	if strings.TrimSpace(path) == "" {
		return []Check{{ID: "access-policy", Status: "skipped", Message: "access policy path was not supplied"}}
	}
	if _, err := access.LoadPolicyFile(path); err != nil {
		return []Check{{ID: "access-policy", Status: "failed", Message: err.Error()}}
	}
	return []Check{{ID: "access-policy", Status: "passed", Message: "access policy schema and action scopes are valid", Evidence: "local parser"}}
}

func checkTLS(authMode, certPath, keyPath string) []Check {
	certPath = strings.TrimSpace(certPath)
	keyPath = strings.TrimSpace(keyPath)
	if certPath == "" && keyPath == "" {
		if authMode != "" && authMode != "development" {
			return []Check{{ID: "tls", Status: "failed", Message: "non-development authentication requires both TLS certificate and key"}}
		}
		return []Check{{ID: "tls", Status: "skipped", Message: "TLS certificate and key were not supplied"}}
	}
	if certPath == "" || keyPath == "" {
		return []Check{{ID: "tls", Status: "failed", Message: "TLS certificate and key must be supplied together"}}
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		return []Check{{ID: "tls", Status: "failed", Message: fmt.Sprintf("TLS certificate/key cannot be loaded: %v", err)}}
	}
	return []Check{{ID: "tls", Status: "passed", Message: "TLS certificate and private key load as a pair", Evidence: "local certificate parser"}}
}

func checkProfileMatrix(path string) []Check {
	if strings.TrimSpace(path) == "" {
		return []Check{{ID: "a2a-profile-matrix", Status: "skipped", Message: "A2A Profile matrix path was not supplied"}}
	}
	matrix, err := conformance.LoadMatrix(path)
	if err != nil {
		return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: err.Error()}}
	}
	return []Check{{ID: "a2a-profile-matrix", Status: "passed", Message: fmt.Sprintf("%d recorded profiles contain no MUST failures", len(matrix.Profiles)), Evidence: "strict repository-owned evidence validation; not a complete conformance claim"}}
}
