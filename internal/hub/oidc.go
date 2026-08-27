package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
)

type OIDCMetadata struct {
	Issuer                   string   `json:"issuer"`
	JWKSURI                  string   `json:"jwks_uri"`
	TokenEndpoint            string   `json:"token_endpoint,omitempty"`
	IntrospectionEndpoint    string   `json:"introspection_endpoint,omitempty"`
	RevocationEndpoint       string   `json:"revocation_endpoint,omitempty"`
	TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

type OIDCKeyProvider struct {
	Issuer              string
	Client              *http.Client
	URLPolicy           netpolicy.Policy
	Now                 func() time.Time
	DefaultTTL          time.Duration
	MinimumTTL          time.Duration
	MaximumTTL          time.Duration
	UnknownRefreshFloor time.Duration

	refreshMu          sync.Mutex
	mu                 sync.RWMutex
	metadata           OIDCMetadata
	metadataExpires    time.Time
	keys               map[string]any
	keysExpires        time.Time
	lastUnknownRefresh time.Time
}

type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	KeyType string   `json:"kty"`
	KeyID   string   `json:"kid"`
	Use     string   `json:"use,omitempty"`
	KeyOps  []string `json:"key_ops,omitempty"`
	Curve   string   `json:"crv,omitempty"`
	N       string   `json:"n,omitempty"`
	E       string   `json:"e,omitempty"`
	X       string   `json:"x,omitempty"`
	Y       string   `json:"y,omitempty"`
}

func NewOIDCKeyProvider(issuer string, client *http.Client) *OIDCKeyProvider {
	return &OIDCKeyProvider{
		Issuer: issuer, Client: client, URLPolicy: netpolicy.HTTPSOnlyPolicy(),
		DefaultTTL: 5 * time.Minute, MinimumTTL: time.Minute,
		MaximumTTL: 24 * time.Hour, UnknownRefreshFloor: 30 * time.Second,
		keys: make(map[string]any),
	}
}

func (p *OIDCKeyProvider) Key(ctx context.Context, issuer, keyID string) (any, error) {
	if issuer != p.Issuer || keyID == "" {
		return nil, errors.New("OIDC issuer or key ID is not trusted")
	}
	now := p.now()
	p.mu.RLock()
	key, found := p.keys[keyID]
	hasCachedKeys := len(p.keys) > 0
	fresh := now.Before(p.keysExpires)
	lastUnknown := p.lastUnknownRefresh
	p.mu.RUnlock()
	if found && fresh {
		return key, nil
	}
	unknownRefresh := !found && hasCachedKeys
	if unknownRefresh && !lastUnknown.IsZero() && now.Sub(lastUnknown) < p.unknownRefreshFloor() {
		return nil, errors.New("OIDC signing key is unavailable")
	}
	if err := p.refresh(ctx, unknownRefresh); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	key, found = p.keys[keyID]
	if !found {
		return nil, errors.New("OIDC signing key is unavailable")
	}
	return key, nil
}

func (p *OIDCKeyProvider) Metadata(ctx context.Context) (OIDCMetadata, error) {
	now := p.now()
	p.mu.RLock()
	metadata := p.metadata
	fresh := metadata.Issuer != "" && now.Before(p.metadataExpires)
	p.mu.RUnlock()
	if fresh {
		return metadata, nil
	}
	if err := p.refresh(ctx, false); err != nil {
		return OIDCMetadata{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.metadata, nil
}

func (p *OIDCKeyProvider) refresh(ctx context.Context, unknownKey bool) error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	now := p.now()
	if unknownKey {
		p.mu.RLock()
		lastUnknown := p.lastUnknownRefresh
		p.mu.RUnlock()
		if !lastUnknown.IsZero() && now.Sub(lastUnknown) < p.unknownRefreshFloor() {
			return errors.New("OIDC signing key refresh is rate limited")
		}
	}
	metadata, metadataTTL, err := p.loadMetadata(ctx)
	if err != nil {
		return err
	}
	var document jwksDocument
	keysTTL, err := p.fetchJSON(ctx, metadata.JWKSURI, &document)
	if err != nil {
		return fmt.Errorf("load OIDC JWKS: %w", err)
	}
	keys := make(map[string]any, len(document.Keys))
	for _, value := range document.Keys {
		if value.KeyID == "" || value.Use != "" && value.Use != "sig" || !allowsVerification(value.KeyOps) {
			continue
		}
		key, err := parseJWK(value)
		if err != nil {
			return fmt.Errorf("parse OIDC JWK %q: %w", value.KeyID, err)
		}
		if _, duplicate := keys[value.KeyID]; duplicate {
			return fmt.Errorf("OIDC JWKS contains duplicate key ID %q", value.KeyID)
		}
		keys[value.KeyID] = key
	}
	if len(keys) == 0 {
		return errors.New("OIDC JWKS contains no usable signing keys")
	}
	p.mu.Lock()
	p.metadata = metadata
	p.metadataExpires = now.Add(metadataTTL)
	p.keys = keys
	p.keysExpires = now.Add(keysTTL)
	if unknownKey {
		p.lastUnknownRefresh = now
	}
	p.mu.Unlock()
	return nil
}

func (p *OIDCKeyProvider) loadMetadata(ctx context.Context) (OIDCMetadata, time.Duration, error) {
	now := p.now()
	p.mu.RLock()
	metadata := p.metadata
	expires := p.metadataExpires
	p.mu.RUnlock()
	if metadata.Issuer != "" && now.Before(expires) {
		return metadata, expires.Sub(now), nil
	}
	issuer := strings.TrimRight(p.Issuer, "/")
	if _, err := p.URLPolicy.ValidateURL(issuer); err != nil {
		return OIDCMetadata{}, 0, fmt.Errorf("validate OIDC issuer: %w", err)
	}
	var discovered OIDCMetadata
	ttl, err := p.fetchJSON(ctx, issuer+"/.well-known/openid-configuration", &discovered)
	if err != nil {
		return OIDCMetadata{}, 0, fmt.Errorf("load OIDC discovery metadata: %w", err)
	}
	if discovered.Issuer != p.Issuer {
		return OIDCMetadata{}, 0, errors.New("OIDC discovery issuer does not match configured issuer")
	}
	for _, endpoint := range []string{discovered.JWKSURI, discovered.TokenEndpoint, discovered.IntrospectionEndpoint, discovered.RevocationEndpoint} {
		if endpoint == "" {
			continue
		}
		if _, err := p.URLPolicy.ValidateURL(endpoint); err != nil {
			return OIDCMetadata{}, 0, fmt.Errorf("validate OIDC endpoint: %w", err)
		}
	}
	if discovered.JWKSURI == "" {
		return OIDCMetadata{}, 0, errors.New("OIDC discovery metadata has no jwks_uri")
	}
	return discovered, ttl, nil
}

func (p *OIDCKeyProvider) fetchJSON(ctx context.Context, endpoint string, target any) (time.Duration, error) {
	if _, err := p.URLPolicy.ValidateURL(endpoint); err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	client := p.Client
	if client == nil {
		client = netpolicy.NewHTTPClient(10*time.Second, nil, p.URLPolicy)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return 0, err
	}
	if len(body) > 1<<20 {
		return 0, errors.New("OIDC document exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(target); err != nil {
		return 0, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return 0, errors.New("OIDC document contains trailing data")
	}
	return p.cacheTTL(response.Header), nil
}

func (p *OIDCKeyProvider) cacheTTL(header http.Header) time.Duration {
	ttl := p.DefaultTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	for _, directive := range strings.Split(header.Get("Cache-Control"), ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if found && strings.EqualFold(name, "max-age") {
			seconds, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
			if err == nil && seconds >= 0 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	}
	minimum := p.MinimumTTL
	if minimum <= 0 {
		minimum = time.Minute
	}
	maximum := p.MaximumTTL
	if maximum <= 0 {
		maximum = 24 * time.Hour
	}
	return min(max(ttl, minimum), maximum)
}

func (p *OIDCKeyProvider) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *OIDCKeyProvider) unknownRefreshFloor() time.Duration {
	if p.UnknownRefreshFloor > 0 {
		return p.UnknownRefreshFloor
	}
	return 30 * time.Second
}

func allowsVerification(operations []string) bool {
	if len(operations) == 0 {
		return true
	}
	for _, operation := range operations {
		if operation == "verify" {
			return true
		}
	}
	return false
}

func parseJWK(value jsonWebKey) (any, error) {
	switch value.KeyType {
	case "RSA":
		modulus, err := decodeBase64URL(value.N)
		if err != nil || len(modulus) == 0 {
			return nil, errors.New("invalid RSA modulus")
		}
		exponentBytes, err := decodeBase64URL(value.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return nil, errors.New("invalid RSA exponent")
		}
		exponent := 0
		for _, part := range exponentBytes {
			exponent = exponent<<8 | int(part)
		}
		if exponent < 3 {
			return nil, errors.New("invalid RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}, nil
	case "EC":
		var curve elliptic.Curve
		switch value.Curve {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, errors.New("unsupported EC curve")
		}
		x, err := decodeBase64URL(value.X)
		if err != nil {
			return nil, errors.New("invalid EC x coordinate")
		}
		y, err := decodeBase64URL(value.Y)
		if err != nil {
			return nil, errors.New("invalid EC y coordinate")
		}
		key := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !curve.IsOnCurve(key.X, key.Y) {
			return nil, errors.New("EC point is not on the declared curve")
		}
		return key, nil
	case "OKP":
		if value.Curve != "Ed25519" {
			return nil, errors.New("unsupported OKP curve")
		}
		key, err := decodeBase64URL(value.X)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid Ed25519 public key")
		}
		return ed25519.PublicKey(key), nil
	default:
		return nil, errors.New("unsupported JWK key type")
	}
}

func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("value is empty")
	}
	return base64.RawURLEncoding.DecodeString(value)
}
