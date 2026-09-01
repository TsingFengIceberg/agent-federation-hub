// Command agent-onboard evaluates a remote A2A AgentCard against an
// operator-owned admission policy. It is intentionally provider-agnostic:
// only the public Card and the selected A2A wire profile are inspected.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/agentconfig"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/onboarding"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

func main() {
	cardURL := flag.String("card-url", "", "public AgentCard URL")
	configPath := flag.String("agent-config", "", "optional agent_config.yaml to load policy from")
	agentID := flag.String("agent-id", "", "registration ID to select from --agent-config")
	profilesValue := flag.String("profiles", "JSONRPC", "ordered A2A profiles: JSONRPC, HTTP_JSON, or GRPC")
	requiredSkillsValue := flag.String("required-skills", "", "comma-separated required skill IDs")
	allowedSkillsValue := flag.String("allowed-skills", "", "comma-separated allowed skill IDs")
	requiredSecurityValue := flag.String("required-security-schemes", "", "comma-separated required security scheme names")
	requireStreaming := flag.Bool("require-streaming", false, "require streaming capability")
	requirePush := flag.Bool("require-push", false, "require Push notification capability")
	requireSigned := flag.Bool("require-signed-card", false, "require a verifiable AgentCard signature")
	signatureKeyFile := flag.String("card-signature-key-file", "", "PEM public key used to verify signed AgentCards")
	signatureKeyID := flag.String("card-signature-key-id", "", "trusted AgentCard signature key ID")
	allowPrivate := flag.Bool("allow-private", false, "allow HTTP and private/local Card URLs for development")
	timeout := flag.Duration("timeout", 15*time.Second, "Card discovery timeout")
	output := flag.String("output", "text", "output format: text or json")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	policy, resolvedURL, err := resolvePolicy(*cardURL, *configPath, *agentID, *profilesValue, *requiredSkillsValue, *allowedSkillsValue, *requiredSecurityValue, *requireStreaming, *requirePush, *requireSigned)
	if err != nil {
		fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "configuration", Status: onboarding.CheckFailed, Message: err.Error()}}, Error: err.Error()})
	}
	if err := hub.ValidateAgentCardURL(resolvedURL, *allowPrivate); err != nil {
		fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "card-url", Status: onboarding.CheckFailed, Message: err.Error()}}, Error: err.Error()})
	}
	profiles, err := a2afederation.ParseBindingProfiles(profileNames(policy.Profiles))
	if err != nil {
		fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "profiles", Status: onboarding.CheckFailed, Message: err.Error()}}, Error: err.Error()})
	}
	adapter, err := a2afederation.NewWithProfiles(*timeout, profiles, secrets.NewEnvProvider(nil))
	if err != nil {
		fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "adapter", Status: onboarding.CheckFailed, Message: err.Error()}}, Error: err.Error()})
	}
	if *requireSigned {
		if strings.TrimSpace(*signatureKeyFile) == "" || strings.TrimSpace(*signatureKeyID) == "" {
			fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "card-signature", Status: onboarding.CheckFailed, Message: "--require-signed-card requires --card-signature-key-file and --card-signature-key-id"}}, Error: "signed Card verification key is not configured"})
		}
		encoded, readErr := os.ReadFile(*signatureKeyFile)
		if readErr != nil {
			fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "card-signature", Status: onboarding.CheckFailed, Message: "cannot read signature key"}}, Error: "cannot read signature key"})
		}
		key, parseErr := hub.ParsePublicKeyPEM(encoded)
		if parseErr != nil {
			fatalOutput(*output, onboarding.Report{Version: onboarding.ReportVersion, EvidenceStatus: "configuration", CardURL: resolvedURL, GeneratedAt: time.Now().UTC(), Checks: []onboarding.Check{{ID: "card-signature", Status: onboarding.CheckFailed, Message: "invalid signature key"}}, Error: "invalid signature key"})
		}
		adapter.SetCardVerifier(a2afederation.CardVerifier{Required: true, Resolver: a2afederation.StaticCardSignatureResolver{*signatureKeyID: key}})
	}
	report, runErr := onboarding.Run(ctx, adapter, resolvedURL, policy)
	writeReport(os.Stdout, *output, report)
	if runErr != nil {
		os.Exit(1)
	}
}

func resolvePolicy(cardURL, configPath, selectedID, profilesValue, requiredSkillsValue, allowedSkillsValue, requiredSecurityValue string, requireStreaming, requirePush, requireSigned bool) (onboarding.Policy, string, error) {
	if strings.TrimSpace(configPath) != "" {
		file, err := agentconfig.LoadFile(configPath)
		if err != nil {
			return onboarding.Policy{}, cardURL, err
		}
		if strings.TrimSpace(selectedID) == "" {
			return onboarding.Policy{}, cardURL, errors.New("--agent-id is required with --agent-config")
		}
		for _, registration := range file.Agents {
			if registration.ID != selectedID {
				continue
			}
			profiles := registration.Profiles(file.Defaults)
			policy := onboarding.Policy{Profiles: make([]a2afederation.BindingProfile, 0, len(profiles))}
			for _, profile := range profiles {
				parsed, err := a2afederation.ParseBindingProfiles(profile.Binding)
				if err != nil {
					return policy, registration.CardURL, err
				}
				for _, candidate := range parsed {
					candidate.ProtocolVersion = profile.ProtocolVersion
					candidate.StreamTransport = profile.StreamTransport
					policy.Profiles = append(policy.Profiles, candidate)
				}
			}
			registrationPolicy := registration.RegistrationPolicy(file.Defaults)
			policy.RequiredSkills = append([]string(nil), registrationPolicy.RequiredSkills...)
			policy.AllowedSkills = append([]string(nil), registrationPolicy.AllowedSkills...)
			policy.RequireStreaming = registrationPolicy.RequireStreaming
			policy.RequirePush = registrationPolicy.RequirePushNotifications
			policy.RequiredSecuritySchemes = nil
			return policy, registration.CardURL, nil
		}
		return onboarding.Policy{}, cardURL, fmt.Errorf("agent configuration has no registration %q", selectedID)
	}
	profiles, err := a2afederation.ParseBindingProfiles(profilesValue)
	if err != nil {
		return onboarding.Policy{}, cardURL, err
	}
	return onboarding.Policy{
		Profiles: profiles, RequiredSkills: splitCSV(requiredSkillsValue), AllowedSkills: splitCSV(allowedSkillsValue),
		RequiredSecuritySchemes: splitCSV(requiredSecurityValue), RequireStreaming: requireStreaming,
		RequirePush: requirePush, RequireSignedCard: requireSigned,
	}, cardURL, nil
}

func profileNames(profiles []a2afederation.BindingProfile) string {
	values := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		values = append(values, string(profile.Binding))
	}
	return strings.Join(values, ",")
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func writeReport(writer io.Writer, format string, report onboarding.Report) {
	if format == "json" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_, _ = fmt.Fprintln(writer, string(encoded))
		}
		return
	}
	if report.Agent != nil {
		fmt.Fprintf(writer, "Agent: %s (%s)\nEndpoint: %s\nProfile: %s/%s\n", report.Agent.Name, report.Agent.ProviderVersion, report.Agent.Endpoint, report.Agent.ProtocolVersion, report.Agent.ProtocolBinding)
	}
	fmt.Fprintf(writer, "Evidence: %s\nResult: %s\n", report.EvidenceStatus, map[bool]string{true: "PASSED", false: "FAILED"}[report.Passed])
	for _, check := range report.Checks {
		fmt.Fprintf(writer, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.ID, check.Message)
	}
	if report.Error != "" {
		fmt.Fprintf(writer, "Error: %s\n", report.Error)
	}
}

func fatalOutput(format string, report onboarding.Report) {
	writeReport(os.Stderr, format, report)
	os.Exit(2)
}
