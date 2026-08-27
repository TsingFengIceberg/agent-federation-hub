package hub

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
)

type WorkloadIdentityResolver interface {
	ResolveWorkload(context.Context, string, *x509.Certificate) (access.Principal, error)
}

type StaticWorkloadResolver struct {
	Principals map[string]access.Principal
}

func (r StaticWorkloadResolver) ResolveWorkload(_ context.Context, workloadID string, _ *x509.Certificate) (access.Principal, error) {
	principal, ok := r.Principals[workloadID]
	if !ok {
		return access.Principal{}, access.ErrUnauthenticated
	}
	principal.Scopes = append([]string(nil), principal.Scopes...)
	principal.Roles = append([]string(nil), principal.Roles...)
	principal.Delegation = append([]access.DelegatedActor(nil), principal.Delegation...)
	return principal, nil
}

type MTLSAuthenticator struct {
	Resolver WorkloadIdentityResolver
}

func (a *MTLSAuthenticator) Authenticate(ctx context.Context, request *http.Request) (access.Principal, error) {
	if a.Resolver == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.PeerCertificates) == 0 {
		return access.Principal{}, access.ErrUnauthenticated
	}
	certificate := request.TLS.PeerCertificates[0]
	workloadID, err := spiffeID(certificate)
	if err != nil {
		return access.Principal{}, fmt.Errorf("%w: %v", access.ErrUnauthenticated, err)
	}
	principal, err := a.Resolver.ResolveWorkload(ctx, workloadID, certificate)
	if err != nil || principal.Subject == "" || principal.TenantID == "" {
		return access.Principal{}, access.ErrUnauthenticated
	}
	principal.AuthMethod = "mtls-spiffe"
	if principal.Issuer == "" {
		parsed, _ := url.Parse(workloadID)
		principal.Issuer = "spiffe://" + parsed.Host
	}
	return principal, nil
}

type HybridAuthenticator struct {
	Bearer Authenticator
	MTLS   Authenticator
}

func (a HybridAuthenticator) Authenticate(ctx context.Context, request *http.Request) (access.Principal, error) {
	if strings.TrimSpace(request.Header.Get("Authorization")) != "" {
		if a.Bearer == nil {
			return access.Principal{}, access.ErrUnauthenticated
		}
		return a.Bearer.Authenticate(ctx, request)
	}
	if a.MTLS == nil {
		return access.Principal{}, access.ErrUnauthenticated
	}
	return a.MTLS.Authenticate(ctx, request)
}

func spiffeID(certificate *x509.Certificate) (string, error) {
	var identity string
	for _, candidate := range certificate.URIs {
		if candidate == nil || !strings.EqualFold(candidate.Scheme, "spiffe") {
			continue
		}
		if candidate.Host == "" || candidate.Path == "" || candidate.User != nil || candidate.RawQuery != "" || candidate.Fragment != "" {
			return "", errors.New("client certificate contains an invalid SPIFFE URI SAN")
		}
		if identity != "" {
			return "", errors.New("client certificate contains multiple SPIFFE identities")
		}
		identity = candidate.String()
	}
	if identity == "" {
		return "", errors.New("client certificate has no SPIFFE URI SAN")
	}
	return identity, nil
}
