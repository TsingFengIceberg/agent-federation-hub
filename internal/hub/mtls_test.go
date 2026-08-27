package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
)

func TestMTLSAuthenticatorMapsVerifiedSPIFFEWorkload(t *testing.T) {
	identity, _ := url.Parse("spiffe://partner.example/agents/planner")
	certificate := &x509.Certificate{URIs: []*url.URL{identity}}
	authenticator := &MTLSAuthenticator{Resolver: StaticWorkloadResolver{Principals: map[string]access.Principal{
		identity.String(): {Subject: identity.String(), TenantID: "tenant-a", Scopes: []string{"tasks:read"}},
	}}}
	request := httptest.NewRequest(http.MethodGet, "/v1/tasks/task-1", nil)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
		VerifiedChains:   [][]*x509.Certificate{{certificate}},
	}
	principal, err := authenticator.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != identity.String() || principal.TenantID != "tenant-a" || principal.AuthMethod != "mtls-spiffe" {
		t.Fatalf("principal=%+v", principal)
	}
}

func TestMTLSAuthenticatorRejectsUnverifiedOrAmbiguousIdentity(t *testing.T) {
	identityOne, _ := url.Parse("spiffe://partner.example/agents/one")
	identityTwo, _ := url.Parse("spiffe://partner.example/agents/two")
	authenticator := &MTLSAuthenticator{Resolver: StaticWorkloadResolver{Principals: map[string]access.Principal{}}}
	for _, state := range []*tls.ConnectionState{
		{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{identityOne}}}},
		{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{identityOne, identityTwo}}}, VerifiedChains: [][]*x509.Certificate{{{}}}},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.TLS = state
		if _, err := authenticator.Authenticate(context.Background(), request); err == nil {
			t.Fatal("invalid mTLS identity accepted")
		}
	}
}

func TestHybridAuthenticatorDoesNotDowngradeInvalidBearerToMTLS(t *testing.T) {
	identity, _ := url.Parse("spiffe://partner.example/agents/planner")
	certificate := &x509.Certificate{URIs: []*url.URL{identity}}
	hybrid := HybridAuthenticator{
		Bearer: &JWTAuthenticator{},
		MTLS: &MTLSAuthenticator{Resolver: StaticWorkloadResolver{Principals: map[string]access.Principal{
			identity.String(): {Subject: identity.String(), TenantID: "tenant-a", Scopes: []string{"*"}},
		}}},
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
	if _, err := hybrid.Authenticate(context.Background(), request); err == nil {
		t.Fatal("invalid bearer credential downgraded to mTLS")
	}
}
