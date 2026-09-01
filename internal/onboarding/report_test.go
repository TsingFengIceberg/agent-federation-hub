package onboarding

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestEvaluatePassesConfiguredAdmissionPolicy(t *testing.T) {
	descriptor := federation.Descriptor{
		Name: "research", ProtocolVersion: "1.0", ProtocolBinding: "JSONRPC",
		Streaming: true, PushNotifications: true, CardSignatureVerified: true,
		SecuritySchemes: []string{"oauth"}, Skills: []string{"research", "summarize"},
	}
	checks := Evaluate(descriptor, Policy{
		Profiles:       []a2afederation.BindingProfile{{ProtocolVersion: "1.0", Binding: a2a.TransportProtocolJSONRPC, StreamTransport: "SSE"}},
		RequiredSkills: []string{"research"}, AllowedSkills: []string{"research", "summarize"},
		RequiredSecuritySchemes: []string{"oauth"}, RequireStreaming: true,
		RequirePush: true, RequireSignedCard: true,
	})
	if !allPassed(checks) {
		t.Fatalf("checks=%+v", checks)
	}
}

func TestEvaluateRejectsMissingAndDisallowedCapabilities(t *testing.T) {
	checks := Evaluate(federation.Descriptor{ProtocolVersion: "1.0", ProtocolBinding: "HTTP_JSON", Skills: []string{"other"}}, Policy{
		Profiles:       []a2afederation.BindingProfile{{ProtocolVersion: "1.0", Binding: a2a.TransportProtocolJSONRPC, StreamTransport: "SSE"}},
		RequiredSkills: []string{"research"}, AllowedSkills: []string{"research"},
		RequiredSecuritySchemes: []string{"oauth"}, RequireStreaming: true,
	})
	if allPassed(checks) {
		t.Fatalf("failed policy unexpectedly passed: %+v", checks)
	}
	for _, check := range checks {
		if check.ID == "a2a-profile" || check.ID == "skills" || check.ID == "security-schemes" || check.ID == "streaming" {
			if check.Status != CheckFailed {
				t.Fatalf("check=%+v, want failed", check)
			}
		}
	}
}

func TestRunRecordsDiscoveryFailure(t *testing.T) {
	report, err := Run(context.Background(), nil, "https://agent.example/card", Policy{})
	if err == nil || report.Passed || report.Error == "" || len(report.Checks) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRunDoesNotClaimSuccessWhenAdapterFails(t *testing.T) {
	adapter := discoveryOnlyAdapter{err: errors.New("remote unavailable")}
	report, err := Run(context.Background(), adapter, "https://agent.example/card", Policy{})
	if err == nil || report.Passed || report.EvidenceStatus != "runtime-observation" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

type discoveryOnlyAdapter struct{ err error }

func (a discoveryOnlyAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{}, a.err
}
func (discoveryOnlyAdapter) Send(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
	return func(func(federation.Observation, error) bool) {}
}
func (discoveryOnlyAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{}, errors.New("not implemented")
}
func (discoveryOnlyAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{}, errors.New("not implemented")
}
func (discoveryOnlyAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(func(federation.Observation, error) bool) {}
}
