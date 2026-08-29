// Command trust-probe validates a deployed partner trust profile without
// exposing credentials. It exercises OIDC discovery/JWKS validation, an
// optional HTTPS PDP, central audit export, and optional SPIFFE mTLS.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
)

func main() {
	issuer := flag.String("issuer", "", "OIDC issuer URL")
	audience := flag.String("audience", "", "JWT audience")
	tokenFile := flag.String("token-file", "", "file containing a partner-issued Bearer token")
	pdpURL := flag.String("pdp-url", "", "optional HTTPS PDP decision endpoint")
	auditURL := flag.String("audit-url", "", "optional HTTPS central audit endpoint")
	auditTokenEnv := flag.String("audit-token-env", "", "optional environment variable containing the audit token")
	mtlsURL := flag.String("mtls-url", "", "optional HTTPS mTLS probe URL")
	mtlsCA := flag.String("mtls-ca-file", "", "CA bundle for the mTLS probe")
	mtlsCert := flag.String("mtls-cert-file", "", "client certificate for the mTLS probe")
	mtlsKey := flag.String("mtls-key-file", "", "client private key for the mTLS probe")
	flag.Parse()
	if err := run(*issuer, *audience, *tokenFile, *pdpURL, *auditURL, *auditTokenEnv, *mtlsURL, *mtlsCA, *mtlsCert, *mtlsKey); err != nil {
		fmt.Fprintf(os.Stderr, "trust probe failed: %v\n", err)
		os.Exit(1)
	}
}

func run(issuer, audience, tokenFile, pdpURL, auditURL, auditTokenEnv, mtlsURL, mtlsCA, mtlsCert, mtlsKey string) error {
	if issuer == "" || audience == "" || tokenFile == "" {
		return errors.New("--issuer, --audience, and --token-file are required")
	}
	token, err := readSecretFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read token file: %w", err)
	}
	provider := hub.NewOIDCKeyProvider(issuer, nil)
	store, err := core.OpenJournal("")
	if err != nil {
		return err
	}
	defer store.Close()
	authenticator := &hub.JWTAuthenticator{Issuer: issuer, Audience: audience, Keys: provider, Revocations: store, RequireTokenID: true}
	request, err := http.NewRequest(http.MethodGet, "https://trust-probe.invalid/", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	principal, err := authenticator.Authenticate(context.Background(), request)
	if err != nil {
		return fmt.Errorf("OIDC/JWKS validation: %w", err)
	}
	fmt.Printf("OIDC/JWKS: pass (issuer=%s subject=%s tenant=%s)\n", issuer, principal.Subject, principal.TenantID)

	if pdpURL != "" {
		policy := netpolicy.HTTPSOnlyPolicy()
		if _, err := policy.ValidateURL(pdpURL); err != nil {
			return fmt.Errorf("PDP URL: %w", err)
		}
		authorizer := &access.HTTPAuthorizer{Endpoint: pdpURL, Client: netpolicy.NewHTTPClient(10*time.Second, nil, policy)}
		if err := authorizer.Authorize(context.Background(), principal, access.Request{Action: access.ActionTaskSubmit}); err != nil {
			return fmt.Errorf("PDP authorization: %w", err)
		}
		fmt.Println("PDP: pass")
	}
	if auditURL != "" {
		var bearer func(context.Context) (string, error)
		if auditTokenEnv != "" {
			bearer = func(context.Context) (string, error) {
				value := strings.TrimSpace(os.Getenv(auditTokenEnv))
				if value == "" {
					return "", errors.New("audit token environment variable is empty")
				}
				return value, nil
			}
		}
		sink, err := access.NewHTTPAuditSink(auditURL, bearer)
		if err != nil {
			return err
		}
		if err := sink.Record(context.Background(), access.AuditRecord{RequestID: core.NewID(), Decision: "trust_probe", Action: access.ActionTaskSubmit, Subject: principal.Subject, TenantID: principal.TenantID, Issuer: principal.Issuer, AuthMethod: principal.AuthMethod}); err != nil {
			return fmt.Errorf("central audit export: %w", err)
		}
		fmt.Println("central audit: pass")
	}
	if mtlsURL != "" {
		if mtlsCA == "" || mtlsCert == "" || mtlsKey == "" {
			return errors.New("mTLS probe requires --mtls-ca-file, --mtls-cert-file, and --mtls-key-file")
		}
		caBytes, err := os.ReadFile(mtlsCA)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caBytes) {
			return errors.New("mTLS CA bundle contains no certificates")
		}
		certificate, err := tls.LoadX509KeyPair(mtlsCert, mtlsKey)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}}
		response, err := client.Get(mtlsURL)
		if err != nil {
			return fmt.Errorf("mTLS probe: %w", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("mTLS probe returned HTTP %d", response.StatusCode)
		}
		fmt.Println("SPIFFE mTLS: pass")
	}
	return nil
}

func readSecretFile(path string) (string, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(encoded))
	if value == "" || len(value) > 64<<10 {
		return "", errors.New("token file is empty or too large")
	}
	return value, nil
}
