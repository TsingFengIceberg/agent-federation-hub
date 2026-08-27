package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTAuthenticatorValidatesIdentityAndIgnoresForgedTenantHeader(t *testing.T) {
	verifier, privateKey, now := newTestJWTAuthenticator(t)
	token := signTestJWT(t, privateKey, now, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "federation-hub", "sub": "user-1",
		"tenant_id": "tenant-from-token", "scope": "agents:read tasks:read",
		"roles": []string{"operator"}, "jti": "token-1",
		"delegation": []map[string]any{{"sub": "end-user", "iss": "https://upstream.example"}},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(TenantHeader, "forged-tenant")
	principal, err := verifier.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != "tenant-from-token" || principal.Subject != "user-1" || principal.TokenID != "token-1" {
		t.Fatalf("principal=%+v", principal)
	}
	if len(principal.Delegation) != 1 || principal.Delegation[0].Subject != "end-user" {
		t.Fatalf("delegation=%+v", principal.Delegation)
	}
}

func TestJWTAuthenticatorRejectsInvalidStandardClaimsAndSignature(t *testing.T) {
	verifier, privateKey, now := newTestJWTAuthenticator(t)
	_, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	tests := []struct {
		name   string
		key    ed25519.PrivateKey
		claims jwt.MapClaims
	}{
		{name: "expired", key: privateKey, claims: jwt.MapClaims{"iss": "https://issuer.example", "aud": "federation-hub", "sub": "u", "tenant_id": "t", "exp": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Hour).Unix()}},
		{name: "issuer", key: privateKey, claims: jwt.MapClaims{"iss": "https://other.example", "aud": "federation-hub", "sub": "u", "tenant_id": "t"}},
		{name: "audience", key: privateKey, claims: jwt.MapClaims{"iss": "https://issuer.example", "aud": "other", "sub": "u", "tenant_id": "t"}},
		{name: "signature", key: otherPrivateKey, claims: jwt.MapClaims{"iss": "https://issuer.example", "aud": "federation-hub", "sub": "u", "tenant_id": "t"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := signTestJWT(t, test.key, now, test.claims)
			request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			if _, err := verifier.Authenticate(context.Background(), request); err == nil {
				t.Fatal("invalid JWT accepted")
			}
		})
	}
}

func TestHTTPAuthorizationAndAuditRedactCredentials(t *testing.T) {
	verifier, privateKey, now := newTestJWTAuthenticator(t)
	token := signTestJWT(t, privateKey, now, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "federation-hub", "sub": "user-1",
		"tenant_id": "tenant-a", "scope": "agents:read",
	})
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var audit bytes.Buffer
	handler := (&HTTPHandler{
		Service:       newTestService(t, store, &fakeAdapter{}),
		Authenticator: verifier, Authorizer: access.DefaultScopeAuthorizer(),
		Audit: access.NewJSONAuditSink(&audit), Now: func() time.Time { return now },
	}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(`{"secret":"body-secret"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(TenantHeader, "forged-tenant")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	records := audit.String()
	if strings.Contains(records, token) || strings.Contains(records, "body-secret") {
		t.Fatalf("audit leaked credential or payload: %s", records)
	}
	if !strings.Contains(records, `"tenantId":"tenant-a"`) || !strings.Contains(records, `"decision":"authorization_denied"`) {
		t.Fatalf("audit records=%s", records)
	}
}

func newTestJWTAuthenticator(t *testing.T) (*JWTAuthenticator, ed25519.PrivateKey, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	return &JWTAuthenticator{
		Issuer: "https://issuer.example", Audience: "federation-hub",
		Algorithms: []string{"EdDSA"}, Keys: StaticKeyProvider{Keys: map[string]any{"key-1": publicKey}},
		Now: func() time.Time { return now },
	}, privateKey, now
}

func signTestJWT(t *testing.T, privateKey ed25519.PrivateKey, now time.Time, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = now.Add(-time.Minute).Unix()
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = now.Add(time.Hour).Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
