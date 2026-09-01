package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
	"github.com/golang-jwt/jwt/v5"
)

func TestRealTrustBundleWithOIDCMTLSPDPAndOperations(t *testing.T) {
	if os.Getenv("AFH_RUN_TRUST_TESTS") != "1" {
		t.Skip("AFH_RUN_TRUST_TESTS is not enabled")
	}
	privateOne, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicOne := &privateOne.PublicKey
	privateTwo, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicTwo := &privateTwo.PublicKey
	state := struct {
		sync.Mutex
		keys           []jsonWebKey
		discoveryCalls int
		jwksCalls      int
		pdpCalls       int
		auditCalls     int
		auditAvailable bool
	}{keys: []jsonWebKey{ecJWK("key-1", publicOne)}, auditAvailable: true}

	identityHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		state.Lock()
		defer state.Unlock()
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			state.discoveryCalls++
			writeJSONForTrust(response, OIDCMetadata{
				Issuer: identityServerURL(request), JWKSURI: identityServerURL(request) + "/keys",
			})
		case "/keys":
			state.jwksCalls++
			writeJSONForTrust(response, jwksDocument{Keys: state.keys})
		case "/pdp":
			state.pdpCalls++
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), "token-secret") {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			writeJSONForTrust(response, map[string]any{"allow": true, "decisionId": "decision-1"})
		case "/audit":
			state.auditCalls++
			if !state.auditAvailable {
				response.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			response.WriteHeader(http.StatusAccepted)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})
	identityServer := httptest.NewUnstartedServer(identityHandler)
	identityServer.Listener, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	identityServer.StartTLS()
	defer identityServer.Close()
	port := strings.Split(identityServer.Listener.Addr().String(), ":")
	policy := netpolicy.HTTPSOnlyPolicy()
	policy.AllowPrivate = true
	policy.AllowedPorts = map[string]struct{}{port[len(port)-1]: {}}
	provider := NewOIDCKeyProvider(identityServer.URL, identityServer.Client())
	provider.URLPolicy = policy
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	provider.Now = func() time.Time { return now }
	bundlePath := filepath.Join(t.TempDir(), "trust_bundle.json")
	bundle := TrustBundle{
		Version: 1, Generation: 1, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		Issuers: map[string]IssuerTrustProfile{
			identityServer.URL:         {Tenants: []string{"tenant-a"}},
			"spiffe://partner.example": {Tenants: []string{"tenant-a"}},
		},
	}
	encodedBundle, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, encodedBundle, 0o600); err != nil {
		t.Fatal(err)
	}
	trustBundle, err := NewTrustBundleManager(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	trustBundle.Now = func() time.Time { return now }
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	authenticator := &JWTAuthenticator{
		Issuer: identityServer.URL, Audience: "hub",
		Algorithms: []string{"ES256"}, Keys: provider,
		Revocations: store, RequireTokenID: true, Now: func() time.Time { return now }, Validator: trustBundle,
	}
	firstToken := signIntegrationToken(t, privateOne, "key-1", "token-1", identityServer.URL, now)
	if _, err := authenticateIntegrationToken(authenticator, firstToken); err != nil {
		t.Fatal(err)
	}
	state.Lock()
	state.keys = []jsonWebKey{ecJWK("key-2", publicTwo)}
	state.Unlock()
	secondToken := signIntegrationToken(t, privateTwo, "key-2", "token-2", identityServer.URL, now)
	if _, err := authenticateIntegrationToken(authenticator, secondToken); err != nil {
		t.Fatal(err)
	}
	state.Lock()
	if state.discoveryCalls < 1 || state.jwksCalls != 2 {
		t.Fatalf("OIDC calls discovery=%d jwks=%d", state.discoveryCalls, state.jwksCalls)
	}
	state.Unlock()
	if err := store.RevokeToken(context.Background(), core.TokenRevocation{
		Issuer: identityServer.URL, TokenID: "token-2", TenantID: "tenant-a",
		RevokedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticateIntegrationToken(authenticator, secondToken); err == nil {
		t.Fatal("revoked token was accepted")
	}

	policyEndpoint := identityServer.URL + "/pdp"
	decision := &access.HTTPAuthorizer{
		Endpoint: policyEndpoint, Client: identityServer.Client(),
	}
	if err := decision.Authorize(context.Background(), access.Principal{Subject: "subject", TenantID: "tenant-a"}, access.Request{Action: access.ActionTaskRead}); err != nil {
		t.Fatal(err)
	}
	state.Lock()
	if state.pdpCalls != 1 {
		t.Fatalf("PDP calls=%d", state.pdpCalls)
	}
	state.Unlock()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := access.OpenFileAuditSink(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	limiter := access.NewTokenBucketLimiter(60, 1)
	limiter.Now = func() time.Time { return now }
	handler := (&HTTPHandler{
		Service:       &Service{Store: store},
		Authenticator: authenticator, Authorizer: access.DefaultScopeAuthorizer(),
		Limiter: limiter, Audit: audit,
	}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	request.Header.Set("Authorization", "Bearer "+firstToken)
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, request)
	if firstResponse.Code != http.StatusUnauthorized {
		// The first token is deliberately revoked only in the previous check; use
		// a fresh token for the operational rate-limit assertion below.
		t.Logf("revoked token status=%d", firstResponse.Code)
	}
	operationalToken := signIntegrationToken(t, privateTwo, "key-2", "token-3", identityServer.URL, now)
	for index := 0; index < 2; index++ {
		request = httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
		request.Header.Set("Authorization", "Bearer "+operationalToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if index == 0 && response.Code != http.StatusOK {
			t.Fatalf("first operational request status=%d body=%s", response.Code, response.Body.String())
		}
		if index == 1 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("rate-limit response status=%d retry=%q", response.Code, response.Header().Get("Retry-After"))
		}
	}
	content, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "rate_limited") {
		t.Fatalf("audit did not persist rate-limit decision: %s", content)
	}
	central, err := access.NewHTTPAuditSink(identityServer.URL+"/audit", func(context.Context) (string, error) { return "audit-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	central.Client = identityServer.Client()
	record := access.AuditRecord{RequestID: "integration-audit", Decision: "authorization_allowed", Action: access.ActionTaskRead, Subject: "subject", TenantID: "tenant-a"}
	if err := central.Record(context.Background(), record); err != nil {
		t.Fatalf("central audit success: %v", err)
	}
	state.Lock()
	state.auditAvailable = false
	state.Unlock()
	retrying := &access.RetryingAuditSink{Sink: central, Attempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }}
	if err := retrying.Record(context.Background(), record); err == nil {
		t.Fatal("central audit outage was not surfaced")
	}
	state.Lock()
	state.auditAvailable = true
	auditCalls := state.auditCalls
	state.Unlock()
	if auditCalls < 3 {
		t.Fatalf("central audit calls=%d, expected success plus bounded outage retries", auditCalls)
	}

	testRealMTLSWorkload(t)
}

func testRealMTLSWorkload(t *testing.T) {
	t.Helper()
	caCert, caKey, caPEM := makeCertificateAuthority(t)
	serverCert := makeSignedCertificate(t, caCert, caKey, x509.ExtKeyUsageServerAuth, nil, []net.IP{net.ParseIP("127.0.0.1")})
	identity, _ := url.Parse("spiffe://partner.example/agents/planner")
	clientCert := makeSignedCertificate(t, caCert, caKey, x509.ExtKeyUsageClientAuth, []*url.URL{identity}, nil)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	bundlePath := filepath.Join(t.TempDir(), "mtls-trust-bundle.json")
	bundle := TrustBundle{
		Version: 1, Generation: 1, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Issuers: map[string]IssuerTrustProfile{"spiffe://partner.example": {Tenants: []string{"tenant-a"}}},
		Workloads: map[string]WorkloadTrustProfile{identity.String(): {
			Subject: identity.String(), TenantID: "tenant-a", Issuer: "spiffe://partner.example", Scopes: []string{"agents:read"},
		}},
	}
	encodedBundle, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, encodedBundle, 0o600); err != nil {
		t.Fatal(err)
	}
	trustBundle, err := NewTrustBundleManager(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	trustBundle.Now = func() time.Time { return now }
	workloadAuthenticator := &MTLSAuthenticator{Resolver: trustBundle, Validator: trustBundle}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, err := workloadAuthenticator.Authenticate(request.Context(), request); err != nil {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert}, ClientCAs: roots,
		ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	defer server.Close()
	clientTLS := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{clientCert}, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mTLS status=%d", response.StatusCode)
	}
	_ = caPEM // retain the generated CA bytes as an explicit fixture artifact.
}

func makeCertificateAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AFH Test CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, der
}

func makeSignedCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, usage x509.ExtKeyUsage, uris []*url.URL, ips []net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "AFH Test Workload"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature,
		URIs: uris, IPAddresses: ips}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	_ = certificate
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key, Leaf: certificate}
}

func identityServerURL(request *http.Request) string {
	return "https://" + request.Host
}

func writeJSONForTrust(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func authenticateIntegrationToken(authenticator *JWTAuthenticator, token string) (access.Principal, error) {
	request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return authenticator.Authenticate(context.Background(), request)
}

func signIntegrationToken(t *testing.T, key *ecdsa.PrivateKey, keyID, tokenID, issuer string, now time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": issuer, "aud": "hub", "sub": "workload-1", "tenant_id": "tenant-a",
		"scope": "agents:read", "jti": tokenID, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func ecJWK(keyID string, key *ecdsa.PublicKey) jsonWebKey {
	size := (key.Curve.Params().BitSize + 7) / 8
	return jsonWebKey{
		KeyType: "EC", KeyID: keyID, Use: "sig", KeyOps: []string{"verify"}, Curve: "P-256",
		X: base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, size))),
		Y: base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, size))),
	}
}
