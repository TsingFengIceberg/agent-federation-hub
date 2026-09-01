package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
)

func TestTenantTrustPolicyBindsIssuerToTenantAndScope(t *testing.T) {
	document := TenantTrustDocument{Version: 1, Issuers: map[string]IssuerTrustProfile{
		"https://issuer.example": {Tenants: []string{"tenant-a"}, RequiredScopes: []string{"hub:invoke"}},
	}}
	policy, err := NewTrustPolicy(document)
	if err != nil {
		t.Fatal(err)
	}
	principal := access.Principal{Issuer: "https://issuer.example", TenantID: "tenant-a", Scopes: []string{"hub:invoke"}}
	if err := policy.ValidatePrincipal(context.Background(), principal); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []access.Principal{
		{Issuer: "https://issuer.example", TenantID: "tenant-b", Scopes: []string{"hub:invoke"}},
		{Issuer: "https://other.example", TenantID: "tenant-a", Scopes: []string{"hub:invoke"}},
		{Issuer: "https://issuer.example", TenantID: "tenant-a"},
	} {
		if err := policy.ValidatePrincipal(context.Background(), invalid); err == nil {
			t.Fatalf("invalid principal accepted: %+v", invalid)
		}
	}
}

func TestLoadTenantTrustFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"issuers":{},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTenantTrustFile(path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestRepositoryTenantTrustExampleRemainsValid(t *testing.T) {
	if _, err := LoadTenantTrustFile("../../tenant_trust.example.json"); err != nil {
		t.Fatalf("load tenant_trust.example.json: %v", err)
	}
}
