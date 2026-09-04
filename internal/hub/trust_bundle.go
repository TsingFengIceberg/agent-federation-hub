package hub

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/transport"
	"github.com/a2aproject/a2a-go/v2/a2a"
)

const TrustBundleVersion = 1

// TrustBundle is the operator-distributed trust snapshot for a Hub. It is
// deliberately data-only: identity providers, certificate authorities, and
// policy services remain outside the Hub process.
//
// The file must be delivered through a trusted configuration channel. The Hub
// validates generation and time bounds to prevent accidental rollback or use
// of an expired snapshot, but it is not itself a signed configuration format.
type TrustBundle struct {
	Version    int                             `json:"version"`
	Generation uint64                          `json:"generation"`
	NotBefore  time.Time                       `json:"notBefore"`
	ExpiresAt  time.Time                       `json:"expiresAt"`
	Issuers    map[string]IssuerTrustProfile   `json:"issuers"`
	Workloads  map[string]WorkloadTrustProfile `json:"workloads,omitempty"`
	// CardKeys contains operator-distributed public keys for signed AgentCards.
	// Private signing keys never enter the Hub or this bundle.
	CardKeys map[string]string `json:"cardKeys,omitempty"`
	// CardKeyPolicies optionally constrains the lifecycle of each Card signing
	// key. Omitting a policy preserves the original key-map semantics; when a
	// policy is present, verification fails closed outside its validity window
	// or after revocation.
	CardKeyPolicies map[string]CardKeyPolicy `json:"cardKeyPolicies,omitempty"`
}

// CardKeyPolicy is an operator-declared validity and revocation window for an
// AgentCard signing key. It intentionally carries no private key material.
type CardKeyPolicy struct {
	NotBefore time.Time `json:"notBefore"`
	ExpiresAt time.Time `json:"expiresAt"`
	Revoked   bool      `json:"revoked,omitempty"`
}

// TrustBundleSource is the distribution boundary for operator-owned trust
// snapshots. Implementations return the exact JSON bytes and, when the
// manager has a verifier configured, the detached signature bytes for those
// same bytes. The source never receives private signing material.
type TrustBundleSource interface {
	Fetch(context.Context) (bundle, signature []byte, err error)
}

// HTTPSignedTrustBundleSource fetches a Trust Bundle and its detached
// signature over an operator-authenticated HTTPS connection. It is intended
// for a real control-plane distribution service; local files remain useful
// for development and offline recovery.
type HTTPSignedTrustBundleSource struct {
	BundleURL        string
	SignatureURL     string
	Client           *http.Client
	URLPolicy        netpolicy.Policy
	Bearer           func(context.Context) (string, error)
	MaxResponseBytes int64
}

// SetHTTPClientWithPolicy installs custom TLS/pooling while retaining the
// caller's explicit environment policy for the distribution endpoint.
func (s *HTTPSignedTrustBundleSource) SetHTTPClientWithPolicy(client *http.Client, policy netpolicy.Policy) {
	if s != nil && client != nil {
		s.URLPolicy = policy
		s.Client = netpolicy.WithURLPolicy(client, nil, policy)
	}
}

func NewHTTPSignedTrustBundleSource(bundleURL, signatureURL string, bearer func(context.Context) (string, error)) (*HTTPSignedTrustBundleSource, error) {
	policy := netpolicy.HTTPSBaseURLPolicy()
	bundle, err := policy.ValidateBaseURL(strings.TrimSpace(bundleURL))
	if err != nil {
		return nil, fmt.Errorf("Trust Bundle URL must be an HTTPS base URL without user, query, or fragment: %w", err)
	}
	signature, err := policy.ValidateBaseURL(strings.TrimSpace(signatureURL))
	if err != nil {
		return nil, fmt.Errorf("Trust Bundle signature URL must be an HTTPS base URL without user, query, or fragment: %w", err)
	}
	if !netpolicy.SameOrigin(bundle, signature) {
		return nil, errors.New("Trust Bundle URL and signature URL must use the same HTTPS origin")
	}
	return &HTTPSignedTrustBundleSource{
		BundleURL:    strings.TrimRight(bundle.String(), "/"),
		SignatureURL: strings.TrimRight(signature.String(), "/"),
		Bearer:       bearer, Client: netpolicy.NewHTTPClient(10*time.Second, nil, policy), URLPolicy: policy, MaxResponseBytes: 1 << 20,
	}, nil
}

func (s *HTTPSignedTrustBundleSource) Fetch(ctx context.Context) ([]byte, []byte, error) {
	if s == nil || s.BundleURL == "" || s.SignatureURL == "" {
		return nil, nil, errors.New("HTTPS Trust Bundle source is not configured")
	}
	bundle, err := s.fetch(ctx, s.BundleURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch Trust Bundle: %w", err)
	}
	signature, err := s.fetch(ctx, s.SignatureURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch Trust Bundle signature: %w", err)
	}
	return bundle, signature, nil
}

func (s *HTTPSignedTrustBundleSource) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	if _, err := s.urlPolicy().ValidateBaseURL(endpoint); err != nil {
		return nil, fmt.Errorf("validate Trust Bundle distribution URL: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if s.Bearer != nil {
		token, resolveErr := s.Bearer(ctx)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if strings.TrimSpace(token) == "" {
			return nil, errors.New("Trust Bundle credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := s.Client
	if client == nil {
		client = netpolicy.NewHTTPClient(10*time.Second, nil, s.urlPolicy())
	} else {
		// Distribution callers may install custom roots or mTLS, but the custom
		// transport cannot opt out of the outbound URL/DNS policy.
		client = netpolicy.WithURLPolicy(client, nil, s.urlPolicy())
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	encoded, err := transport.ReadBounded(response.Body, s.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 {
		return nil, errors.New("response is empty")
	}
	return encoded, nil
}

func (s *HTTPSignedTrustBundleSource) urlPolicy() netpolicy.Policy {
	if s != nil && (s.URLPolicy.AllowPrivate || s.URLPolicy.AllowHTTP || s.URLPolicy.AllowedPorts != nil || s.URLPolicy.MaxRedirects != 0) {
		return s.URLPolicy
	}
	return netpolicy.HTTPSBaseURLPolicy()
}

// WorkloadTrustProfile maps one verified SPIFFE URI SAN to the Hub Principal
// used by authorization. TLS chain verification remains the HTTP server's
// responsibility; this mapping adds tenant and policy identity.
type WorkloadTrustProfile struct {
	Subject    string                  `json:"subject"`
	TenantID   string                  `json:"tenantId"`
	Issuer     string                  `json:"issuer,omitempty"`
	Scopes     []string                `json:"scopes,omitempty"`
	Roles      []string                `json:"roles,omitempty"`
	Delegation []access.DelegatedActor `json:"delegation,omitempty"`
}

func LoadTrustBundleFile(path string) (TrustBundle, error) {
	if strings.TrimSpace(path) == "" {
		return TrustBundle{}, errors.New("trust bundle file path is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return TrustBundle{}, fmt.Errorf("read trust bundle file: %w", err)
	}
	if len(encoded) > 1<<20 {
		return TrustBundle{}, errors.New("trust bundle file exceeds 1 MiB")
	}
	return decodeTrustBundle(encoded)
}

func decodeTrustBundle(encoded []byte) (TrustBundle, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var bundle TrustBundle
	if err := decoder.Decode(&bundle); err != nil {
		return TrustBundle{}, fmt.Errorf("decode trust bundle file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return TrustBundle{}, errors.New("trust bundle file contains trailing data")
	}
	if err := bundle.Validate(); err != nil {
		return TrustBundle{}, err
	}
	return bundle, nil
}

func (b TrustBundle) Validate() error {
	if b.Version != TrustBundleVersion {
		return fmt.Errorf("trust bundle version must be %d", TrustBundleVersion)
	}
	if b.Generation == 0 {
		return errors.New("trust bundle generation must be positive")
	}
	if b.NotBefore.IsZero() || b.ExpiresAt.IsZero() || !b.ExpiresAt.After(b.NotBefore) {
		return errors.New("trust bundle requires an ordered notBefore and expiresAt")
	}
	if len(b.Issuers) == 0 {
		return errors.New("trust bundle must configure at least one issuer")
	}
	for issuer, profile := range b.Issuers {
		if err := validateTrustIssuer(issuer); err != nil {
			return fmt.Errorf("trust bundle issuer %q: %w", issuer, err)
		}
		if err := (TenantTrustDocument{Version: 1, Issuers: map[string]IssuerTrustProfile{issuer: profile}}).Validate(); err != nil {
			return err
		}
	}
	for workloadID, profile := range b.Workloads {
		if err := validateSPIFFEIdentity(workloadID); err != nil {
			return fmt.Errorf("trust bundle workload %q: %w", workloadID, err)
		}
		if strings.TrimSpace(profile.Subject) == "" || strings.TrimSpace(profile.TenantID) == "" {
			return fmt.Errorf("trust bundle workload %q requires subject and tenantId", workloadID)
		}
		issuer := profile.Issuer
		if issuer == "" {
			parsed, parseErr := url.Parse(workloadID)
			if parseErr != nil {
				return fmt.Errorf("trust bundle workload %q: invalid workload URI", workloadID)
			}
			issuer = "spiffe://" + parsed.Host
		}
		if err := validateTrustIssuer(issuer); err != nil {
			return fmt.Errorf("trust bundle workload %q issuer: %w", workloadID, err)
		}
		issuerProfile, trustedIssuer := b.Issuers[issuer]
		if !trustedIssuer {
			return fmt.Errorf("trust bundle workload %q issuer %q is not configured", workloadID, issuer)
		}
		allowedTenant := false
		for _, tenant := range issuerProfile.Tenants {
			if tenant == profile.TenantID {
				allowedTenant = true
				break
			}
		}
		if !allowedTenant {
			return fmt.Errorf("trust bundle workload %q tenant %q is not trusted for issuer", workloadID, profile.TenantID)
		}
		if profile.Issuer != "" {
			if err := validateTrustIssuer(profile.Issuer); err != nil {
				return fmt.Errorf("trust bundle workload %q issuer: %w", workloadID, err)
			}
		}
		if err := validateUniqueStrings(profile.Scopes, "scope"); err != nil {
			return fmt.Errorf("trust bundle workload %q: %w", workloadID, err)
		}
		if err := validateUniqueStrings(profile.Roles, "role"); err != nil {
			return fmt.Errorf("trust bundle workload %q: %w", workloadID, err)
		}
		for index, actor := range profile.Delegation {
			if strings.TrimSpace(actor.Subject) == "" {
				return fmt.Errorf("trust bundle workload %q delegation[%d] has no subject", workloadID, index)
			}
		}
	}
	for keyID, encoded := range b.CardKeys {
		if strings.TrimSpace(keyID) == "" || len(keyID) > 256 {
			return errors.New("trust bundle Card key IDs must be non-empty and at most 256 bytes")
		}
		if _, err := ParsePublicKeyPEM([]byte(encoded)); err != nil {
			return fmt.Errorf("trust bundle Card key %q: %w", keyID, err)
		}
	}
	for keyID, policy := range b.CardKeyPolicies {
		if strings.TrimSpace(keyID) == "" || len(keyID) > 256 {
			return errors.New("trust bundle Card key policy IDs must be non-empty and at most 256 bytes")
		}
		if _, exists := b.CardKeys[keyID]; !exists {
			return fmt.Errorf("trust bundle Card key policy %q has no corresponding cardKeys entry", keyID)
		}
		if policy.NotBefore.IsZero() || policy.ExpiresAt.IsZero() || !policy.ExpiresAt.After(policy.NotBefore) {
			return fmt.Errorf("trust bundle Card key policy %q requires an ordered notBefore and expiresAt", keyID)
		}
	}
	return nil
}

func (b TrustBundle) ValidateAt(now time.Time) error {
	if err := b.Validate(); err != nil {
		return err
	}
	now = now.UTC()
	if now.Before(b.NotBefore.UTC()) {
		return errors.New("trust bundle is not active yet")
	}
	if !now.Before(b.ExpiresAt.UTC()) {
		return errors.New("trust bundle has expired")
	}
	return nil
}

func validateTrustIssuer(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("issuer must be an absolute URI without user, query, or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "spiffe" {
		return errors.New("issuer must use HTTPS or SPIFFE")
	}
	return nil
}

func validateSPIFFEIdentity(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.Path == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("workload identity must be a SPIFFE URI with host and path")
	}
	return nil
}

func validateUniqueStrings(values []string, label string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s list contains an empty value", label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s list repeats %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// TrustBundleManager atomically swaps validated snapshots. A provider may
// replace the file and then call Reload, or use Watch for bounded polling.
// Failed reloads retain the last valid snapshot; an expired snapshot rejects
// authentication through ValidatePrincipal/ResolveWorkload.
type TrustBundleManager struct {
	path       string
	signature  string
	source     TrustBundleSource
	verifier   *TrustBundleSignatureVerifier
	current    atomic.Value // stores TrustBundle
	mu         sync.Mutex
	digest     [32]byte
	hasDigest  bool
	generation uint64
	Now        func() time.Time
}

func NewTrustBundleManager(path string) (*TrustBundleManager, error) {
	return newTrustBundleManager(path, "", nil)
}

// NewSignedTrustBundleManager enables detached signature verification for the
// initial snapshot and every later reload. The signature file contains a
// base64url (or standard base64) signature over the exact JSON bytes.
func NewSignedTrustBundleManager(path, signaturePath, publicKeyPath string) (*TrustBundleManager, error) {
	verifier, err := loadTrustBundleSignature(signaturePath, publicKeyPath)
	if err != nil {
		return nil, err
	}
	return newTrustBundleManager(path, signaturePath, &verifier)
}

// NewSignedTrustBundleManagerFromSource creates a manager backed by a
// protected distribution source. A detached signature verifier is mandatory
// so a successful HTTPS response cannot be mistaken for an authorized trust
// snapshot merely because the transport was encrypted.
func NewSignedTrustBundleManagerFromSource(source TrustBundleSource, publicKeyPath string) (*TrustBundleManager, error) {
	if source == nil {
		return nil, errors.New("Trust Bundle source is required")
	}
	verifier, err := loadTrustBundlePublicKey(publicKeyPath)
	if err != nil {
		return nil, err
	}
	manager := &TrustBundleManager{source: source, verifier: &verifier}
	if _, err := manager.Reload(); err != nil {
		return nil, err
	}
	return manager, nil
}

func newTrustBundleManager(path, signaturePath string, verifier *TrustBundleSignatureVerifier) (*TrustBundleManager, error) {
	manager := &TrustBundleManager{path: strings.TrimSpace(path), signature: strings.TrimSpace(signaturePath), verifier: verifier}
	if manager.path == "" {
		return nil, errors.New("trust bundle file path is required")
	}
	if _, err := manager.Reload(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *TrustBundleManager) Snapshot() (TrustBundle, bool) {
	if m == nil {
		return TrustBundle{}, false
	}
	value := m.current.Load()
	if value == nil {
		return TrustBundle{}, false
	}
	return cloneTrustBundle(value.(TrustBundle)), true
}

func (m *TrustBundleManager) Reload() (bool, error) {
	return m.reload(context.Background())
}

func (m *TrustBundleManager) reload(ctx context.Context) (bool, error) {
	if m == nil || m.path == "" {
		if m == nil || m.source == nil {
			return false, errors.New("trust bundle manager is not configured")
		}
	}
	var encoded, signature []byte
	var err error
	if m.source != nil {
		encoded, signature, err = m.source.Fetch(ctx)
	} else {
		encoded, err = os.ReadFile(m.path)
	}
	if err != nil {
		return false, err
	}
	if len(encoded) > 1<<20 {
		return false, errors.New("trust bundle file exceeds 1 MiB")
	}
	if m.verifier != nil {
		if m.source == nil {
			signature, err = os.ReadFile(m.signature)
			if err != nil {
				return false, fmt.Errorf("read trust bundle signature: %w", err)
			}
		}
		if len(signature) > 64<<10 {
			return false, errors.New("trust bundle signature exceeds 64 KiB")
		}
		if verifyErr := m.verifier.Verify(encoded, signature); verifyErr != nil {
			return false, verifyErr
		}
	}
	digest := sha256.Sum256(encoded)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hasDigest && digest == m.digest {
		return false, nil
	}
	bundle, err := decodeTrustBundle(encoded)
	if err != nil {
		return false, err
	}
	if m.generation != 0 && bundle.Generation <= m.generation {
		return false, fmt.Errorf("trust bundle generation %d is not newer than %d", bundle.Generation, m.generation)
	}
	m.current.Store(bundle)
	m.generation = bundle.Generation
	m.digest = digest
	m.hasDigest = true
	return true, nil
}

func (m *TrustBundleManager) Watch(ctx context.Context, interval time.Duration, report func(error)) {
	if m == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.reload(ctx); err != nil && report != nil {
				report(err)
			}
		}
	}
}

func (m *TrustBundleManager) ValidatePrincipal(_ context.Context, principal access.Principal) error {
	bundle, ok := m.Snapshot()
	if !ok {
		return errors.New("trust bundle is unavailable")
	}
	if err := bundle.ValidateAt(m.now()); err != nil {
		return err
	}
	profile, ok := bundle.Issuers[principal.Issuer]
	if !ok {
		return errors.New("principal issuer is not trusted")
	}
	allowedTenant := false
	for _, tenant := range profile.Tenants {
		if tenant == principal.TenantID {
			allowedTenant = true
			break
		}
	}
	if !allowedTenant {
		return errors.New("principal tenant is not trusted for issuer")
	}
	for _, scope := range profile.RequiredScopes {
		if !principal.HasScope(scope) {
			return fmt.Errorf("principal is missing trust scope %q", scope)
		}
	}
	return nil
}

func (m *TrustBundleManager) ResolveWorkload(_ context.Context, workloadID string, _ *x509.Certificate) (access.Principal, error) {
	bundle, ok := m.Snapshot()
	if !ok {
		return access.Principal{}, access.ErrUnauthenticated
	}
	if err := bundle.ValidateAt(m.now()); err != nil {
		return access.Principal{}, access.ErrUnauthenticated
	}
	profile, ok := bundle.Workloads[workloadID]
	if !ok {
		return access.Principal{}, access.ErrUnauthenticated
	}
	issuer := profile.Issuer
	if issuer == "" {
		parsed, err := url.Parse(workloadID)
		if err != nil {
			return access.Principal{}, access.ErrUnauthenticated
		}
		issuer = "spiffe://" + parsed.Host
	}
	return access.Principal{
		Subject: profile.Subject, TenantID: profile.TenantID, Issuer: issuer,
		Scopes: append([]string(nil), profile.Scopes...), Roles: append([]string(nil), profile.Roles...),
		Delegation: append([]access.DelegatedActor(nil), profile.Delegation...),
	}, nil
}

// ValidateCurrent is suitable for readiness checks. Authentication remains
// fail-closed when a bundle later expires, even if readiness is not queried.
func (m *TrustBundleManager) ValidateCurrent() error {
	bundle, ok := m.Snapshot()
	if !ok {
		return errors.New("trust bundle is unavailable")
	}
	return bundle.ValidateAt(m.now())
}

func (m *TrustBundleManager) IssuerConfigured(issuer string) bool {
	bundle, ok := m.Snapshot()
	if !ok {
		return false
	}
	_, ok = bundle.Issuers[strings.TrimSpace(issuer)]
	return ok
}

// ResolveCardKey implements the A2A CardSignatureResolver boundary using the
// latest validated Trust Bundle snapshot. The AgentCard argument is accepted
// for interface compatibility; key selection is by the signed JWS kid and the
// operator-distributed bundle, never by an untrusted URL in the Card.
func (m *TrustBundleManager) ResolveCardKey(_ context.Context, _ *a2a.AgentCard, keyID string) (crypto.PublicKey, error) {
	if m == nil {
		return nil, errors.New("trust bundle manager is unavailable")
	}
	bundle, ok := m.Snapshot()
	if !ok {
		return nil, errors.New("trust bundle is unavailable")
	}
	if err := bundle.ValidateAt(m.now()); err != nil {
		return nil, fmt.Errorf("trust bundle is not active: %w", err)
	}
	keyID = strings.TrimSpace(keyID)
	encoded, ok := bundle.CardKeys[keyID]
	if !ok {
		return nil, fmt.Errorf("AgentCard signing key %q is not trusted", keyID)
	}
	if policy, constrained := bundle.CardKeyPolicies[keyID]; constrained {
		now := m.now()
		if policy.Revoked {
			return nil, fmt.Errorf("AgentCard signing key %q is revoked", keyID)
		}
		if now.Before(policy.NotBefore.UTC()) {
			return nil, fmt.Errorf("AgentCard signing key %q is not active yet", keyID)
		}
		if !now.Before(policy.ExpiresAt.UTC()) {
			return nil, fmt.Errorf("AgentCard signing key %q has expired", keyID)
		}
	}
	return ParsePublicKeyPEM([]byte(encoded))
}

// Generation returns the active snapshot generation for diagnostics and
// readiness logging. The boolean reports whether a snapshot is loaded.
func (m *TrustBundleManager) Generation() (uint64, bool) {
	bundle, ok := m.Snapshot()
	if !ok {
		return 0, false
	}
	return bundle.Generation, true
}

func (m *TrustBundleManager) HasWorkloads() bool {
	bundle, ok := m.Snapshot()
	return ok && len(bundle.Workloads) > 0
}

func (m *TrustBundleManager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func cloneTrustBundle(bundle TrustBundle) TrustBundle {
	clone := bundle
	clone.Issuers = make(map[string]IssuerTrustProfile, len(bundle.Issuers))
	for issuer, profile := range bundle.Issuers {
		profile.Tenants = append([]string(nil), profile.Tenants...)
		profile.RequiredScopes = append([]string(nil), profile.RequiredScopes...)
		clone.Issuers[issuer] = profile
	}
	clone.Workloads = make(map[string]WorkloadTrustProfile, len(bundle.Workloads))
	for workload, profile := range bundle.Workloads {
		profile.Scopes = append([]string(nil), profile.Scopes...)
		profile.Roles = append([]string(nil), profile.Roles...)
		profile.Delegation = append([]access.DelegatedActor(nil), profile.Delegation...)
		clone.Workloads[workload] = profile
	}
	clone.CardKeys = make(map[string]string, len(bundle.CardKeys))
	for keyID, encoded := range bundle.CardKeys {
		clone.CardKeys[keyID] = encoded
	}
	clone.CardKeyPolicies = make(map[string]CardKeyPolicy, len(bundle.CardKeyPolicies))
	for keyID, policy := range bundle.CardKeyPolicies {
		clone.CardKeyPolicies[keyID] = policy
	}
	return clone
}
