package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type oidcRoundTripper struct {
	mu              sync.Mutex
	discoveryIssuer string
	keys            []jsonWebKey
	discoveryCalls  int
	jwksCalls       int
}

func (transport *oidcRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	var payload any
	switch request.URL.Path {
	case "/.well-known/openid-configuration":
		transport.discoveryCalls++
		payload = OIDCMetadata{
			Issuer: transport.discoveryIssuer, JWKSURI: "https://issuer.example/keys",
			TokenEndpoint: "https://issuer.example/token",
		}
	case "/keys":
		transport.jwksCalls++
		payload = jwksDocument{Keys: transport.keys}
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not found"))}, nil
	}
	encoded, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Cache-Control": []string{"public, max-age=300"}},
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
	}, nil
}

func TestOIDCDiscoveryJWKSCacheAndKeyRotation(t *testing.T) {
	publicOne, privateOne, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicTwo, privateTwo, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transport := &oidcRoundTripper{
		discoveryIssuer: "https://issuer.example",
		keys:            []jsonWebKey{ed25519JWK("key-1", publicOne)},
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	provider := NewOIDCKeyProvider("https://issuer.example", &http.Client{Transport: transport})
	provider.Now = func() time.Time { return now }
	authenticator := &JWTAuthenticator{
		Issuer: "https://issuer.example", Audience: "federation-hub",
		Algorithms: []string{"EdDSA"}, Keys: provider, Now: func() time.Time { return now },
	}

	authenticateOIDCToken(t, authenticator, signOIDCToken(t, privateOne, "key-1", now))
	authenticateOIDCToken(t, authenticator, signOIDCToken(t, privateOne, "key-1", now))
	transport.mu.Lock()
	if transport.discoveryCalls != 1 || transport.jwksCalls != 1 {
		t.Fatalf("cache calls discovery=%d jwks=%d", transport.discoveryCalls, transport.jwksCalls)
	}
	transport.keys = []jsonWebKey{ed25519JWK("key-2", publicTwo)}
	transport.mu.Unlock()

	authenticateOIDCToken(t, authenticator, signOIDCToken(t, privateTwo, "key-2", now))
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.jwksCalls != 2 {
		t.Fatalf("unknown kid did not refresh JWKS: calls=%d", transport.jwksCalls)
	}
}

func TestOIDCDiscoveryRejectsIssuerMismatch(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transport := &oidcRoundTripper{
		discoveryIssuer: "https://attacker.example",
		keys:            []jsonWebKey{ed25519JWK("key-1", publicKey)},
	}
	provider := NewOIDCKeyProvider("https://issuer.example", &http.Client{Transport: transport})
	if _, err := provider.Metadata(context.Background()); err == nil {
		t.Fatal("mismatched discovery issuer accepted")
	}
}

func TestParseOIDCJWKSupportsRSAAndECDSA(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaJWK := jsonWebKey{
		KeyType: "RSA", KeyID: "rsa-1",
		N: base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}
	parsedRSA, err := parseJWK(rsaJWK)
	if err != nil {
		t.Fatal(err)
	}
	if key, ok := parsedRSA.(*rsa.PublicKey); !ok || key.N.Cmp(rsaKey.N) != 0 || key.E != rsaKey.E {
		t.Fatalf("parsed RSA key=%+v", parsedRSA)
	}

	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	coordinateSize := (ecdsaKey.Curve.Params().BitSize + 7) / 8
	x := ecdsaKey.X.FillBytes(make([]byte, coordinateSize))
	y := ecdsaKey.Y.FillBytes(make([]byte, coordinateSize))
	parsedECDSA, err := parseJWK(jsonWebKey{
		KeyType: "EC", KeyID: "ec-1", Curve: "P-256",
		X: base64.RawURLEncoding.EncodeToString(x), Y: base64.RawURLEncoding.EncodeToString(y),
	})
	if err != nil {
		t.Fatal(err)
	}
	if key, ok := parsedECDSA.(*ecdsa.PublicKey); !ok || key.X.Cmp(ecdsaKey.X) != 0 || key.Y.Cmp(ecdsaKey.Y) != 0 {
		t.Fatalf("parsed ECDSA key=%+v", parsedECDSA)
	}
}

func ed25519JWK(keyID string, key ed25519.PublicKey) jsonWebKey {
	return jsonWebKey{
		KeyType: "OKP", KeyID: keyID, Use: "sig", KeyOps: []string{"verify"},
		Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(key),
	}
}

func signOIDCToken(t *testing.T, key ed25519.PrivateKey, keyID string, now time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "https://issuer.example", "aud": "federation-hub", "sub": "workload-1",
		"tenant_id": "tenant-a", "scope": "tasks:read", "jti": "token-1",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func authenticateOIDCToken(t *testing.T, authenticator *JWTAuthenticator, token string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/tasks/task-1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	principal, err := authenticator.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != "tenant-a" || principal.Subject != "workload-1" {
		t.Fatalf("principal=%+v", principal)
	}
}
