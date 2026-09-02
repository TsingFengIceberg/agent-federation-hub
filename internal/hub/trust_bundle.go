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
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
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
	current    atomic.Value // stores TrustBundle
	mu         sync.Mutex
	digest     [32]byte
	hasDigest  bool
	generation uint64
	Now        func() time.Time
}

func NewTrustBundleManager(path string) (*TrustBundleManager, error) {
	manager := &TrustBundleManager{path: strings.TrimSpace(path)}
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
	if m == nil || m.path == "" {
		return false, errors.New("trust bundle manager is not configured")
	}
	encoded, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read trust bundle file: %w", err)
	}
	if len(encoded) > 1<<20 {
		return false, errors.New("trust bundle file exceeds 1 MiB")
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
			if _, err := m.Reload(); err != nil && report != nil {
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
	encoded, ok := bundle.CardKeys[strings.TrimSpace(keyID)]
	if !ok {
		return nil, fmt.Errorf("AgentCard signing key %q is not trusted", keyID)
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
	return clone
}
