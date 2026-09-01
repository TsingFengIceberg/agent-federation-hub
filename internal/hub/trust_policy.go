package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
)

// TenantTrustDocument binds accepted issuers to explicit tenants and optional
// trust scopes. This prevents a valid token from an otherwise trusted issuer
// from asserting an arbitrary tenant.
type TenantTrustDocument struct {
	Version int                           `json:"version"`
	Issuers map[string]IssuerTrustProfile `json:"issuers"`
}

type IssuerTrustProfile struct {
	Tenants        []string `json:"tenants"`
	RequiredScopes []string `json:"requiredScopes,omitempty"`
}

func LoadTenantTrustFile(path string) (TenantTrustDocument, error) {
	if strings.TrimSpace(path) == "" {
		return TenantTrustDocument{}, errors.New("tenant trust file path is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return TenantTrustDocument{}, fmt.Errorf("read tenant trust file: %w", err)
	}
	if len(encoded) > 1<<20 {
		return TenantTrustDocument{}, errors.New("tenant trust file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document TenantTrustDocument
	if err := decoder.Decode(&document); err != nil {
		return TenantTrustDocument{}, fmt.Errorf("decode tenant trust file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return TenantTrustDocument{}, errors.New("tenant trust file contains trailing data")
	}
	if err := document.Validate(); err != nil {
		return TenantTrustDocument{}, err
	}
	return document, nil
}

func (d TenantTrustDocument) Validate() error {
	if d.Version != 1 {
		return errors.New("tenant trust version must be 1")
	}
	if len(d.Issuers) == 0 {
		return errors.New("tenant trust must configure at least one issuer")
	}
	for issuer, profile := range d.Issuers {
		if strings.TrimSpace(issuer) == "" || len(profile.Tenants) == 0 {
			return errors.New("tenant trust issuer must have a name and tenant list")
		}
		seen := make(map[string]struct{}, len(profile.Tenants))
		for _, tenant := range profile.Tenants {
			if strings.TrimSpace(tenant) == "" {
				return fmt.Errorf("tenant trust issuer %q contains an empty tenant", issuer)
			}
			if _, exists := seen[tenant]; exists {
				return fmt.Errorf("tenant trust issuer %q repeats tenant %q", issuer, tenant)
			}
			seen[tenant] = struct{}{}
		}
		for _, scope := range profile.RequiredScopes {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf("tenant trust issuer %q contains an empty required scope", issuer)
			}
		}
	}
	return nil
}

// TrustPolicy implements PrincipalValidator for both JWT and mTLS
// authenticators. Issuer and tenant matching are exact and fail closed.
type TrustPolicy struct {
	Issuers map[string]IssuerTrustProfile
}

func NewTrustPolicy(document TenantTrustDocument) (TrustPolicy, error) {
	if err := document.Validate(); err != nil {
		return TrustPolicy{}, err
	}
	profiles := make(map[string]IssuerTrustProfile, len(document.Issuers))
	for issuer, profile := range document.Issuers {
		profile.Tenants = append([]string(nil), profile.Tenants...)
		profile.RequiredScopes = append([]string(nil), profile.RequiredScopes...)
		profiles[issuer] = profile
	}
	return TrustPolicy{Issuers: profiles}, nil
}

func (p TrustPolicy) ValidatePrincipal(_ context.Context, principal access.Principal) error {
	profile, ok := p.Issuers[principal.Issuer]
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
