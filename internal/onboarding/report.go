// Package onboarding contains provider admission checks that are independent
// of the provider's implementation or runtime. The Hub only evaluates the
// public AgentCard and operator-owned policy; it never inspects provider
// prompts, tools, memory, or workflow state.
package onboarding

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
)

const ReportVersion = 1

const (
	CheckPassed  = "passed"
	CheckFailed  = "failed"
	CheckSkipped = "skipped"
)

// Policy is the local admission policy applied to a public AgentCard.
// Profiles are ordered by preference and must be supported by the adapter.
type Policy struct {
	Profiles                []a2afederation.BindingProfile
	RequiredSkills          []string
	AllowedSkills           []string
	RequiredSecuritySchemes []string
	RequireStreaming        bool
	RequirePush             bool
	RequireSignedCard       bool
}

type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Report intentionally contains only the public Card-derived descriptor and
// check results. Credential references and credential values are never copied
// into the report.
type Report struct {
	Version        int                    `json:"version"`
	EvidenceStatus string                 `json:"evidenceStatus"`
	CardURL        string                 `json:"cardUrl"`
	GeneratedAt    time.Time              `json:"generatedAt"`
	Agent          *federation.Descriptor `json:"agent,omitempty"`
	Checks         []Check                `json:"checks"`
	Passed         bool                   `json:"passed"`
	Error          string                 `json:"error,omitempty"`
}

// Run discovers a Card through the existing A2A adapter and evaluates it.
// A network or protocol failure is represented in the report so callers can
// persist a useful admission record while still receiving a non-nil error.
func Run(ctx context.Context, adapter federation.Adapter, cardURL string, policy Policy) (Report, error) {
	report := Report{
		Version:        ReportVersion,
		EvidenceStatus: "runtime-observation",
		CardURL:        strings.TrimSpace(cardURL),
		GeneratedAt:    time.Now().UTC(),
		Checks:         []Check{},
	}
	if adapter == nil {
		report.Checks = append(report.Checks, Check{ID: "adapter", Status: CheckFailed, Message: "A2A adapter is required"})
		report.Error = "A2A adapter is required"
		return report, fmt.Errorf("A2A adapter is required")
	}
	if strings.TrimSpace(cardURL) == "" {
		report.Checks = append(report.Checks, Check{ID: "card-url", Status: CheckFailed, Message: "AgentCard URL is required"})
		report.Error = "AgentCard URL is required"
		return report, fmt.Errorf("AgentCard URL is required")
	}
	descriptor, err := adapter.Discover(ctx, cardURL)
	if err != nil {
		report.Checks = append(report.Checks, Check{ID: "agent-card-discovery", Status: CheckFailed, Message: safeError(err)})
		report.Error = safeError(err)
		return report, err
	}
	report.Agent = &descriptor
	report.Checks = Evaluate(descriptor, policy)
	report.Passed = allPassed(report.Checks)
	if !report.Passed {
		return report, fmt.Errorf("AgentCard admission checks failed")
	}
	return report, nil
}

// Evaluate checks a previously discovered descriptor. It is exported so
// configuration validation, CLI tooling, and unit tests share identical
// policy semantics without performing a second network request.
func Evaluate(descriptor federation.Descriptor, policy Policy) []Check {
	checks := make([]Check, 0, 7)
	checks = append(checks, Check{ID: "agent-card-discovery", Status: CheckPassed, Message: "AgentCard was discovered and parsed"})
	if len(policy.Profiles) == 0 {
		checks = append(checks, Check{ID: "a2a-profile", Status: CheckSkipped, Message: "no local A2A Profile constraint was requested"})
	} else if profileMatches(descriptor, policy.Profiles) {
		checks = append(checks, Check{ID: "a2a-profile", Status: CheckPassed, Message: fmt.Sprintf("advertises %s/%s", descriptor.ProtocolVersion, descriptor.ProtocolBinding)})
	} else {
		checks = append(checks, Check{ID: "a2a-profile", Status: CheckFailed, Message: "advertised interface does not match any configured A2A Profile"})
	}
	if policy.RequireStreaming {
		checks = append(checks, capabilityCheck("streaming", descriptor.Streaming, "streaming is advertised", "streaming is required but not advertised"))
	} else {
		checks = append(checks, Check{ID: "streaming", Status: CheckSkipped, Message: "streaming is not required by local policy"})
	}
	if policy.RequirePush {
		checks = append(checks, capabilityCheck("push-notifications", descriptor.PushNotifications, "Push notifications are advertised", "Push notifications are required but not advertised"))
	} else {
		checks = append(checks, Check{ID: "push-notifications", Status: CheckSkipped, Message: "Push notifications are not required by local policy"})
	}
	if len(policy.RequiredSkills) == 0 && len(policy.AllowedSkills) == 0 {
		checks = append(checks, Check{ID: "skills", Status: CheckSkipped, Message: "no skill constraint was requested"})
	} else {
		missing := missingStrings(policy.RequiredSkills, descriptor.Skills)
		disallowed := disallowedStrings(policy.AllowedSkills, descriptor.Skills)
		switch {
		case len(missing) > 0:
			checks = append(checks, Check{ID: "skills", Status: CheckFailed, Message: "required skills missing: " + strings.Join(missing, ", ")})
		case len(disallowed) > 0:
			checks = append(checks, Check{ID: "skills", Status: CheckFailed, Message: "skills outside allow-list: " + strings.Join(disallowed, ", ")})
		default:
			checks = append(checks, Check{ID: "skills", Status: CheckPassed, Message: "declared skills satisfy local policy"})
		}
	}
	if len(policy.RequiredSecuritySchemes) == 0 {
		checks = append(checks, Check{ID: "security-schemes", Status: CheckSkipped, Message: "no security scheme constraint was requested"})
	} else if missing := missingStrings(policy.RequiredSecuritySchemes, descriptor.SecuritySchemes); len(missing) > 0 {
		checks = append(checks, Check{ID: "security-schemes", Status: CheckFailed, Message: "required security schemes missing: " + strings.Join(missing, ", ")})
	} else {
		checks = append(checks, Check{ID: "security-schemes", Status: CheckPassed, Message: "required security schemes are advertised"})
	}
	if policy.RequireSignedCard {
		checks = append(checks, capabilityCheck("card-signature", descriptor.CardSignatureVerified, "AgentCard signature was verified", "a trusted AgentCard signature is required"))
	} else {
		checks = append(checks, Check{ID: "card-signature", Status: CheckSkipped, Message: "signed AgentCards are not required by local policy"})
	}
	return checks
}

func profileMatches(descriptor federation.Descriptor, profiles []a2afederation.BindingProfile) bool {
	for _, profile := range profiles {
		if profile.ProtocolVersion == descriptor.ProtocolVersion && strings.EqualFold(normalizeBinding(string(profile.Binding)), normalizeBinding(descriptor.ProtocolBinding)) {
			return true
		}
	}
	return false
}

func normalizeBinding(value string) string {
	return strings.ToUpper(strings.NewReplacer("_", "", "-", "", "+", "").Replace(strings.TrimSpace(value)))
}

func capabilityCheck(id string, advertised bool, passMessage, failMessage string) Check {
	if advertised {
		return Check{ID: id, Status: CheckPassed, Message: passMessage}
	}
	return Check{ID: id, Status: CheckFailed, Message: failMessage}
}

func missingStrings(required, declared []string) []string {
	set := make(map[string]struct{}, len(declared))
	for _, value := range declared {
		set[value] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range required {
		if _, ok := set[value]; !ok {
			missing = append(missing, value)
		}
	}
	return missing
}

func disallowedStrings(allowed, declared []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[value] = struct{}{}
	}
	result := make([]string, 0)
	for _, value := range declared {
		if _, ok := set[value]; !ok {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func allPassed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == CheckFailed {
			return false
		}
	}
	return true
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	// Adapter errors are intentionally reduced to their public message. The
	// report must not expose credential references, response bodies, or URLs
	// supplied by a remote service.
	return strings.TrimSpace(err.Error())
}
