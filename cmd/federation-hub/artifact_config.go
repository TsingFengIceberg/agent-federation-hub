package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

type artifactOptions struct {
	Backend              string
	Root                 string
	S3Endpoint           string
	S3Region             string
	S3Bucket             string
	S3Prefix             string
	S3AccessKeyReference string
	S3SecretReference    string
	S3SessionReference   string
	S3Secure             bool
	MaxBytes             int64
	TenantMaxBytes       int64
	TenantMaxObjects     int64
	Retention            time.Duration
	MIMEAllowlist        string
	RequireClean         bool
	Scanner              string
	ClamAVNetwork        string
	ClamAVAddress        string
	AllowPrivateURIs     bool
}

func buildArtifactService(
	ctx context.Context,
	options artifactOptions,
	metadata core.ArtifactMetadataStore,
	provider secrets.Provider,
) (*artifactstore.Service, error) {
	if metadata == nil {
		return nil, errors.New("configured Store does not implement Artifact metadata persistence")
	}
	var objects artifactstore.ObjectStore
	switch options.Backend {
	case "filesystem":
		store, err := artifactstore.NewFileStore(options.Root)
		if err != nil {
			return nil, err
		}
		objects = store
	case "s3":
		accessKey, secretKey, sessionToken := "", "", ""
		if options.S3AccessKeyReference != "" || options.S3SecretReference != "" {
			if options.S3AccessKeyReference == "" || options.S3SecretReference == "" || provider == nil {
				return nil, errors.New("S3 access-key and secret-key references must be configured together")
			}
			var err error
			accessKey, err = provider.Resolve(ctx, options.S3AccessKeyReference)
			if err != nil {
				return nil, errors.New("S3 access key is unavailable")
			}
			secretKey, err = provider.Resolve(ctx, options.S3SecretReference)
			if err != nil {
				return nil, errors.New("S3 secret key is unavailable")
			}
			if options.S3SessionReference != "" {
				sessionToken, err = provider.Resolve(ctx, options.S3SessionReference)
				if err != nil {
					return nil, errors.New("S3 session token is unavailable")
				}
			}
		}
		store, err := artifactstore.NewS3Store(artifactstore.S3Config{
			Endpoint: options.S3Endpoint, Region: options.S3Region,
			Bucket: options.S3Bucket, Prefix: options.S3Prefix,
			AccessKeyID: accessKey, SecretAccessKey: secretKey, SessionToken: sessionToken,
			Secure: options.S3Secure,
		})
		if err != nil {
			return nil, err
		}
		objects = store
	default:
		return nil, fmt.Errorf("unsupported Artifact backend %q", options.Backend)
	}
	allowed, err := artifactstore.ParseMIMEAllowlist(options.MIMEAllowlist)
	if err != nil {
		return nil, fmt.Errorf("parse Artifact MIME allowlist: %w", err)
	}
	var scanner artifactstore.Scanner
	switch options.Scanner {
	case "none":
		scanner = artifactstore.NoopScanner{}
	case "clamav":
		if options.ClamAVAddress == "" {
			return nil, errors.New("ClamAV scanner requires --artifact-clamav-address")
		}
		scanner = artifactstore.ClamAVScanner{
			Network: options.ClamAVNetwork, Address: options.ClamAVAddress,
		}
	default:
		return nil, fmt.Errorf("unsupported Artifact scanner %q", options.Scanner)
	}
	uriPolicy := netpolicy.HTTPSOnlyPolicy()
	uriPolicy.AllowPrivate = options.AllowPrivateURIs
	return &artifactstore.Service{
		Metadata: metadata, Objects: objects, Scanner: scanner,
		Policy: artifactstore.Policy{
			MaxBytes: options.MaxBytes, AllowedMIME: allowed,
			Quota:     artifactstore.Quota{MaxBytes: options.TenantMaxBytes, MaxObjects: options.TenantMaxObjects},
			Retention: options.Retention, RequireClean: options.RequireClean,
		},
		HTTPClient: netpolicy.NewHTTPClient(30*time.Second, nil, uriPolicy),
	}, nil
}
