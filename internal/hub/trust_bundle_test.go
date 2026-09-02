package hub

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/golang-jwt/jwt/v5"
)

func TestTrustBundleFileRejectsUnknownAndTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	valid := trustBundleFixture()
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, []byte("\n{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrustBundleFile(path); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing data error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"generation":1,"notBefore":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z","issuers":{},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrustBundleFile(path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestTrustBundleValidationRejectsInactiveExpiredAndUnboundWorkload(t *testing.T) {
	bundle := trustBundleFixture()
	if err := bundle.ValidateAt(time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("inactive bundle error=%v", err)
	}
	if err := bundle.ValidateAt(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired bundle error=%v", err)
	}
	bundle.Workloads["spiffe://partner.example/ns/prod/sa/unknown"] = WorkloadTrustProfile{
		Subject: "unknown", TenantID: "partner-a", Issuer: "spiffe://untrusted.example",
	}
	if err := bundle.Validate(); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unbound workload error=%v", err)
	}
}

func TestTrustBundleManagerReloadsAtomicallyAndRejectsGenerationRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	bundle := trustBundleFixture()
	writeTrustBundleTestFile(t, path, bundle, 1)
	manager, err := NewTrustBundleManager(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if err := manager.ValidateCurrent(); err != nil {
		t.Fatal(err)
	}
	if snapshot, ok := manager.Snapshot(); !ok || snapshot.Generation != 1 {
		t.Fatalf("initial snapshot=%+v present=%v", snapshot, ok)
	}
	bundle.Generation = 2
	issuerProfile := bundle.Issuers["https://issuer.example"]
	issuerProfile.Tenants = []string{"partner-b"}
	bundle.Issuers["https://issuer.example"] = issuerProfile
	writeTrustBundleTestFile(t, path, bundle, 2)
	loaded, err := manager.Reload()
	if err != nil || !loaded {
		t.Fatalf("reload loaded=%v err=%v", loaded, err)
	}
	snapshot, ok := manager.Snapshot()
	if !ok || snapshot.Generation != 2 || !containsString(snapshot.Issuers["https://issuer.example"].Tenants, "partner-b") {
		t.Fatalf("rotated snapshot=%+v present=%v", snapshot, ok)
	}
	bundle.Generation = 1
	writeTrustBundleTestFile(t, path, bundle, 3)
	if _, err := manager.Reload(); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("rollback error=%v", err)
	}
	snapshot, ok = manager.Snapshot()
	if !ok || snapshot.Generation != 2 {
		t.Fatalf("rollback replaced snapshot=%+v present=%v", snapshot, ok)
	}
	snapshot.Issuers["https://issuer.example"].Tenants[0] = "mutated-locally"
	unchanged, ok := manager.Snapshot()
	if !ok || unchanged.Issuers["https://issuer.example"].Tenants[0] != "partner-b" {
		t.Fatalf("snapshot was not isolated from caller mutation: %+v", unchanged)
	}
}

func TestTrustBundleManagerFeedsOIDCAndMTLSAuthentication(t *testing.T) {
	bundle := trustBundleFixture()
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	writeTrustBundleTestFile(t, path, bundle, 1)
	manager, err := NewTrustBundleManager(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authenticator := &JWTAuthenticator{
		Issuer: "https://issuer.example", Audience: "federation-hub",
		Algorithms: []string{"EdDSA"}, Keys: StaticKeyProvider{Keys: map[string]any{"key-1": publicKey}},
		Validator: manager, Now: func() time.Time { return now },
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "federation-hub", "sub": "operator-1",
		"tenant_id": "partner-a", "scope": "hub:invoke", "iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(), "jti": "trust-bundle-token-1",
	})
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	principal, err := authenticator.Authenticate(context.Background(), request)
	if err != nil || principal.TenantID != "partner-a" {
		t.Fatalf("OIDC principal=%+v err=%v", principal, err)
	}
	identity, _ := url.Parse("spiffe://partner.example/ns/prod/sa/competitive-analysis")
	mtls := &MTLSAuthenticator{Resolver: manager, Validator: manager}
	mtlsRequest := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	mtlsRequest.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{identity}}},
		VerifiedChains:   [][]*x509.Certificate{{{}}},
	}
	workload, err := mtls.Authenticate(context.Background(), mtlsRequest)
	if err != nil || workload.TenantID != "partner-a" || workload.Issuer != "spiffe://partner.example" {
		t.Fatalf("mTLS principal=%+v err=%v", workload, err)
	}
}

func TestRepositoryTrustBundleExampleRemainsValid(t *testing.T) {
	if _, err := LoadTrustBundleFile("../../trust_bundle.example.json"); err != nil {
		t.Fatalf("load trust_bundle.example.json: %v", err)
	}
}

func TestTrustBundleResolvesRotatedAgentCardSigningKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	bundle := trustBundleFixture()
	bundle.CardKeys = map[string]string{"card-key-1": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}))}
	path := filepath.Join(t.TempDir(), "trust_bundle.json")
	writeTrustBundleTestFile(t, path, bundle, 1)
	manager, err := NewTrustBundleManager(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolveCardKey(context.Background(), &a2a.AgentCard{}, "card-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resolved.(crypto.PublicKey); !ok {
		t.Fatalf("resolved key type=%T", resolved)
	}
	if _, err := manager.ResolveCardKey(context.Background(), &a2a.AgentCard{}, "unknown"); err == nil {
		t.Fatal("unknown card key was trusted")
	}
}

func trustBundleFixture() TrustBundle {
	return TrustBundle{
		Version: 1, Generation: 1,
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Issuers: map[string]IssuerTrustProfile{
			"https://issuer.example":   {Tenants: []string{"partner-a"}, RequiredScopes: []string{"hub:invoke"}},
			"spiffe://partner.example": {Tenants: []string{"partner-a"}},
		},
		Workloads: map[string]WorkloadTrustProfile{
			"spiffe://partner.example/ns/prod/sa/competitive-analysis": {
				Subject: "competitive-analysis-agent", TenantID: "partner-a", Issuer: "spiffe://partner.example",
				Scopes: []string{"tasks:submit", "tasks:read"}, Roles: []string{"provider"},
			},
		},
	}
}

func writeTrustBundleTestFile(t *testing.T, path string, bundle TrustBundle, stamp int64) {
	t.Helper()
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1_700_000_000, stamp)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
