// Package preflight provides local, non-destructive deployment checks. It
// deliberately reports configuration evidence separately from production
// qualification, which requires external infrastructure and operational data.
package preflight

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/agentconfig"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

const ReportVersion = 1

type Options struct {
	AgentConfigPath  string
	TrustBundlePath  string
	AccessPolicyPath string
	AuthMode         string
	TLSCertPath      string
	TLSKeyPath       string
	ProfileMatrix    string
	Now              time.Time
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
	report.Checks = append(report.Checks, checkTrustBundle(options.TrustBundlePath, now)...)
	report.Checks = append(report.Checks, checkAccessPolicy(options.AccessPolicyPath)...)
	report.Checks = append(report.Checks, checkTLS(options.AuthMode, options.TLSCertPath, options.TLSKeyPath)...)
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

func checkTrustBundle(path string, now time.Time) []Check {
	if strings.TrimSpace(path) == "" {
		return []Check{{ID: "trust-bundle", Status: "skipped", Message: "Trust Bundle path was not supplied"}}
	}
	bundle, err := hub.LoadTrustBundleFile(path)
	if err != nil {
		return []Check{{ID: "trust-bundle", Status: "failed", Message: err.Error()}}
	}
	if err := bundle.ValidateAt(now); err != nil {
		return []Check{{ID: "trust-bundle", Status: "failed", Message: err.Error()}}
	}
	return []Check{{ID: "trust-bundle", Status: "passed", Message: fmt.Sprintf("Trust Bundle generation %d is valid and active", bundle.Generation), Evidence: "local parser and time-bound validation"}}
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
	encoded, err := os.ReadFile(path)
	if err != nil {
		return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: fmt.Sprintf("read profile matrix: %v", err)}}
	}
	var matrix struct {
		Profiles []struct {
			Binding string `json:"binding"`
			Status  string `json:"status"`
			Failed  int    `json:"tckMustFailed"`
		} `json:"profiles"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	if err := decoder.Decode(&matrix); err != nil {
		return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: fmt.Sprintf("decode profile matrix: %v", err)}}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: "profile matrix contains trailing data"}}
	}
	if len(matrix.Profiles) == 0 {
		return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: "profile matrix contains no profiles"}}
	}
	for _, profile := range matrix.Profiles {
		if profile.Binding == "" || profile.Status == "" || profile.Failed != 0 {
			return []Check{{ID: "a2a-profile-matrix", Status: "failed", Message: "profile matrix contains an incomplete or failed profile"}}
		}
	}
	return []Check{{ID: "a2a-profile-matrix", Status: "passed", Message: fmt.Sprintf("%d recorded profiles contain no MUST failures", len(matrix.Profiles)), Evidence: "repository-owned evidence file; not a complete conformance claim"}}
}
