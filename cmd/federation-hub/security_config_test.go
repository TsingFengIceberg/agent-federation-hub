package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

func TestProductionAuthenticatorRequiresTenantTrustPolicy(t *testing.T) {
	if _, err := buildAuthenticator(context.Background(), securityOptions{AuthMode: "oidc", Issuer: "https://issuer.example", Audience: "hub", RequireTenantTrust: true}, nil); err == nil || !strings.Contains(err.Error(), "tenant-trust-file") {
		t.Fatalf("missing trust policy error=%v", err)
	}
}

func TestProductionAuthorizerRequiresVersionedPolicy(t *testing.T) {
	if _, err := buildAuthorizer(securityOptions{RequireTenantTrust: true}, nil); err == nil || !strings.Contains(err.Error(), "access-policy-file") {
		t.Fatalf("missing access policy error=%v", err)
	}
}

func TestUnifiedTrustBundleReplacesLegacyTrustFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	bundle := hub.TrustBundle{
		Version: 1, Generation: 1,
		NotBefore: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour),
		Issuers: map[string]hub.IssuerTrustProfile{
			"https://issuer.example": {Tenants: []string{"tenant-a"}},
		},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hub.NewTrustBundleManager(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildAuthenticator(context.Background(), securityOptions{
		AuthMode: "oidc", Issuer: "https://issuer.example", Audience: "hub",
		TrustBundleFile: path, TrustBundle: manager, TenantTrustFile: "legacy.json",
	}, nil); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("legacy/unified conflict error=%v", err)
	}
	authenticator, err := buildAuthenticator(context.Background(), securityOptions{
		AuthMode: "oidc", Issuer: "https://issuer.example", Audience: "hub",
		TrustBundleFile: path, TrustBundle: manager,
	}, nil)
	if err != nil || authenticator == nil {
		t.Fatalf("unified authenticator=%v err=%v", authenticator, err)
	}
}

func TestUnifiedTrustBundleRequiresConfiguredMTLSWorkload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	bundle := hub.TrustBundle{
		Version: 1, Generation: 1,
		NotBefore: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour),
		Issuers: map[string]hub.IssuerTrustProfile{
			"spiffe://partner.example": {Tenants: []string{"tenant-a"}},
		},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := hub.NewTrustBundleManager(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildAuthenticator(context.Background(), securityOptions{
		AuthMode: "mtls", TrustBundleFile: path, TrustBundle: manager,
		TLSClientCAFile: "ca.pem",
	}, nil); err == nil || !strings.Contains(err.Error(), "workloads") {
		t.Fatalf("missing workload error=%v", err)
	}
}
