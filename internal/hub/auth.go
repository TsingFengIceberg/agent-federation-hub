package hub

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/golang-jwt/jwt/v5"
)

const (
	TenantHeader     = "X-AFH-Tenant-ID"
	DevSubjectHeader = "X-AFH-Dev-Subject"
)

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (access.Principal, error)
}

type DevelopmentAuthenticator struct{}

func (DevelopmentAuthenticator) Authenticate(_ context.Context, request *http.Request) (access.Principal, error) {
	tenantID := strings.TrimSpace(request.Header.Get(TenantHeader))
	if tenantID == "" {
		return access.Principal{}, access.ErrUnauthenticated
	}
	subject := strings.TrimSpace(request.Header.Get(DevSubjectHeader))
	if subject == "" {
		subject = "development-user"
	}
	return access.Principal{
		Subject: subject, TenantID: tenantID, Issuer: "development",
		AuthMethod: "development-header", Scopes: []string{"*"},
	}, nil
}

type JWTKeyProvider interface {
	Key(context.Context, string, string) (any, error)
}

type StaticKeyProvider struct {
	Keys map[string]any
}

func ParsePublicKeyPEM(encoded []byte) (crypto.PublicKey, error) {
	if key, err := jwt.ParseRSAPublicKeyFromPEM(encoded); err == nil {
		return key, nil
	}
	if key, err := jwt.ParseECPublicKeyFromPEM(encoded); err == nil {
		return key, nil
	}
	if key, err := jwt.ParseEdPublicKeyFromPEM(encoded); err == nil {
		return key, nil
	}
	return nil, errors.New("public key file must contain an RSA, ECDSA, or Ed25519 public key")
}

func (p StaticKeyProvider) Key(_ context.Context, _ string, keyID string) (any, error) {
	key, ok := p.Keys[keyID]
	if !ok {
		return nil, errors.New("JWT signing key is unavailable")
	}
	return key, nil
}

type JWTAuthenticator struct {
	Issuer      string
	Audience    string
	TenantClaim string
	ScopeClaim  string
	RolesClaim  string
	Algorithms  []string
	Keys        JWTKeyProvider
	Leeway      time.Duration
	Now         func() time.Time
}

func (a *JWTAuthenticator) Authenticate(ctx context.Context, request *http.Request) (access.Principal, error) {
	raw, err := bearerToken(request.Header.Get("Authorization"))
	if err != nil || a.Issuer == "" || a.Audience == "" || a.Keys == nil {
		return access.Principal{}, access.ErrUnauthenticated
	}
	algorithms := a.Algorithms
	if len(algorithms) == 0 {
		algorithms = []string{"RS256", "ES256", "EdDSA"}
	}
	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{
		jwt.WithValidMethods(algorithms), jwt.WithIssuer(a.Issuer),
		jwt.WithAudience(a.Audience), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(a.Leeway), jwt.WithStrictDecoding(),
	}
	if a.Now != nil {
		options = append(options, jwt.WithTimeFunc(a.Now))
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" {
			return nil, errors.New("JWT key ID is required")
		}
		return a.Keys.Key(ctx, a.Issuer, keyID)
	}, options...)
	if err != nil || !token.Valid {
		return access.Principal{}, fmt.Errorf("%w: bearer token validation failed", access.ErrUnauthenticated)
	}
	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return access.Principal{}, fmt.Errorf("%w: subject claim is required", access.ErrUnauthenticated)
	}
	tenantClaim := a.TenantClaim
	if tenantClaim == "" {
		tenantClaim = "tenant_id"
	}
	tenantID, ok := claims[tenantClaim].(string)
	if !ok || strings.TrimSpace(tenantID) == "" {
		return access.Principal{}, fmt.Errorf("%w: tenant claim is required", access.ErrUnauthenticated)
	}
	scopeClaim := a.ScopeClaim
	if scopeClaim == "" {
		scopeClaim = "scope"
	}
	rolesClaim := a.RolesClaim
	if rolesClaim == "" {
		rolesClaim = "roles"
	}
	return access.Principal{
		Subject: subject, TenantID: tenantID, Issuer: a.Issuer,
		AuthMethod: "oidc-jwt", Scopes: stringListClaim(claims[scopeClaim], true),
		Roles:      stringListClaim(claims[rolesClaim], false),
		Delegation: delegationClaim(claims["delegation"]),
		TokenID:    stringClaim(claims["jti"]),
	}, nil
}

func bearerToken(value string) (string, error) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("Bearer credential is required")
	}
	return parts[1], nil
}

func stringClaim(value any) string {
	result, _ := value.(string)
	return result
}

func stringListClaim(value any, splitSpace bool) []string {
	if text, ok := value.(string); ok {
		if splitSpace {
			return strings.Fields(text)
		}
		if text != "" {
			return []string{text}
		}
	}
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func delegationClaim(value any) []access.DelegatedActor {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]access.DelegatedActor, 0, len(items))
	for _, item := range items {
		claims, ok := item.(map[string]any)
		if !ok {
			continue
		}
		subject := stringClaim(claims["sub"])
		if subject != "" {
			result = append(result, access.DelegatedActor{Subject: subject, Issuer: stringClaim(claims["iss"])})
		}
	}
	return result
}
