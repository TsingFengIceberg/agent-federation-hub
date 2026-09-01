package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/trust"
)

type securityOptions struct {
	AuthMode             string
	Issuer               string
	Audience             string
	PublicKeyFile        string
	KeyID                string
	WorkloadIdentityFile string
	PolicyURL            string
	PolicyTokenReference string
	TokenProfilesFile    string
	TLSClientCAFile      string
	TenantTrustFile      string
	RequireTenantTrust   bool
	AccessPolicyFile     string
}

func buildSecretProvider(base secrets.Provider, profileFile string) (secrets.Provider, error) {
	if profileFile == "" {
		return base, nil
	}
	var profiles map[string]trust.ExchangeProfile
	if err := readJSONFile(profileFile, &profiles); err != nil {
		return nil, fmt.Errorf("load token exchange profiles: %w", err)
	}
	provider := trust.NewTokenExchangeProvider(base, profiles)
	for name := range profiles {
		if err := provider.ValidateReference(trust.TokenExchangeReferencePrefix + name); err != nil {
			return nil, fmt.Errorf("validate token exchange profile %q: %w", name, err)
		}
	}
	return provider, nil
}

func buildAuthenticator(_ context.Context, options securityOptions, store core.RevocationStore) (hub.Authenticator, error) {
	var validator hub.PrincipalValidator
	if options.TenantTrustFile != "" {
		document, err := hub.LoadTenantTrustFile(options.TenantTrustFile)
		if err != nil {
			return nil, err
		}
		policy, err := hub.NewTrustPolicy(document)
		if err != nil {
			return nil, fmt.Errorf("tenant trust policy: %w", err)
		}
		validator = policy
	} else if options.RequireTenantTrust {
		return nil, errors.New("non-development authentication requires --tenant-trust-file")
	}
	buildOIDC := func() (hub.Authenticator, error) {
		if options.Issuer == "" || options.Audience == "" {
			return nil, errors.New("OIDC authentication requires --jwt-issuer and --jwt-audience")
		}
		provider := hub.NewOIDCKeyProvider(options.Issuer, nil)
		return &hub.JWTAuthenticator{
			Issuer: options.Issuer, Audience: options.Audience, Keys: provider,
			Revocations: store, RequireTokenID: true, Validator: validator,
		}, nil
	}
	buildMTLS := func() (hub.Authenticator, error) {
		if options.WorkloadIdentityFile == "" {
			return nil, errors.New("mTLS authentication requires --workload-identities-file")
		}
		var principals map[string]access.Principal
		if err := readJSONFile(options.WorkloadIdentityFile, &principals); err != nil {
			return nil, fmt.Errorf("load workload identities: %w", err)
		}
		for workloadID, principal := range principals {
			if principal.Subject == "" {
				principal.Subject = workloadID
			}
			if principal.TenantID == "" {
				return nil, fmt.Errorf("workload identity %q has no tenant", workloadID)
			}
			principals[workloadID] = principal
		}
		return &hub.MTLSAuthenticator{Resolver: hub.StaticWorkloadResolver{Principals: principals}, Validator: validator}, nil
	}

	switch options.AuthMode {
	case "development":
		log.Print("WARNING: development header authentication is enabled and is not a production security boundary")
		return hub.DevelopmentAuthenticator{}, nil
	case "oidc":
		return buildOIDC()
	case "mtls":
		return buildMTLS()
	case "oidc-or-mtls":
		bearer, err := buildOIDC()
		if err != nil {
			return nil, err
		}
		certificate, err := buildMTLS()
		if err != nil {
			return nil, err
		}
		return hub.HybridAuthenticator{Bearer: bearer, MTLS: certificate}, nil
	case "jwt", "jwt-static":
		if options.Issuer == "" || options.Audience == "" || options.PublicKeyFile == "" || options.KeyID == "" {
			return nil, errors.New("static JWT mode requires issuer, audience, public key file, and key ID")
		}
		encoded, err := os.ReadFile(options.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read JWT public key: %w", err)
		}
		key, err := hub.ParsePublicKeyPEM(encoded)
		if err != nil {
			return nil, err
		}
		return &hub.JWTAuthenticator{
			Issuer: options.Issuer, Audience: options.Audience,
			Keys:        hub.StaticKeyProvider{Keys: map[string]any{options.KeyID: key}},
			Revocations: store, Validator: validator,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", options.AuthMode)
	}
}

func buildAuthorizer(options securityOptions, provider secrets.Provider) (access.Authorizer, error) {
	local := access.DefaultScopeAuthorizer()
	if options.AccessPolicyFile != "" {
		document, err := access.LoadPolicyFile(options.AccessPolicyFile)
		if err != nil {
			return nil, err
		}
		configured, err := access.NewPolicyAuthorizer(document)
		if err != nil {
			return nil, fmt.Errorf("local access policy: %w", err)
		}
		local = configured
	} else if options.RequireTenantTrust {
		return nil, errors.New("non-development authentication requires --access-policy-file")
	}
	if options.PolicyURL == "" {
		return local, nil
	}
	policy := netpolicy.HTTPSOnlyPolicy()
	policy.MaxRedirects = -1
	if _, err := policy.ValidateURL(options.PolicyURL); err != nil {
		return nil, fmt.Errorf("external policy URL: %w", err)
	}
	if options.PolicyTokenReference != "" {
		if provider == nil {
			return nil, errors.New("external policy credential requires a SecretProvider")
		}
		if err := provider.ValidateReference(options.PolicyTokenReference); err != nil {
			return nil, fmt.Errorf("external policy credential reference: %w", err)
		}
	}
	external := &access.HTTPAuthorizer{
		Endpoint: options.PolicyURL, Client: netpolicy.NewHTTPClient(5*time.Second, nil, policy),
		Bearer: provider, BearerReference: options.PolicyTokenReference,
	}
	return access.ChainAuthorizer{local, external}, nil
}

func buildTLSConfig(authMode, clientCAFile string) (*tls.Config, error) {
	configuration := &tls.Config{MinVersion: tls.VersionTLS12}
	requiresClientCertificate := authMode == "mtls"
	acceptsClientCertificate := requiresClientCertificate || authMode == "oidc-or-mtls"
	if !acceptsClientCertificate {
		return configuration, nil
	}
	if clientCAFile == "" {
		return nil, errors.New("mTLS authentication requires --tls-client-ca-file")
	}
	encoded, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(encoded) {
		return nil, errors.New("client CA bundle contains no certificates")
	}
	configuration.ClientCAs = pool
	if requiresClientCertificate {
		configuration.ClientAuth = tls.RequireAndVerifyClientCert
	} else {
		configuration.ClientAuth = tls.VerifyClientCertIfGiven
	}
	return configuration, nil
}

func readJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 1<<20 {
		return errors.New("JSON file exceeds 1 MiB limit")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON file contains trailing data")
	}
	return nil
}
