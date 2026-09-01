package main

import (
	"context"
	"strings"
	"testing"
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
