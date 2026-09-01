package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/agentconfig"
	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/gateway"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/registry"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/transport"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	journalPath := flag.String("journal", "var/hub.journal", "append-only Hub journal path")
	storageBackend := flag.String("storage", "journal", "storage backend: journal or postgres")
	postgresDSNEnv := flag.String("postgres-dsn-env", "AFH_POSTGRES_DSN", "environment variable containing the PostgreSQL DSN")
	publicBaseURL := flag.String("public-base-url", "", "public HTTPS base URL used for optional A2A Push callbacks")
	agentConfigPath := flag.String("agent-config", "agent_config.yaml", "YAML file containing operator-owned remote Agent registrations")
	allowPrivateAgentURLs := flag.Bool("allow-private-agent-urls", false, "allow HTTP or private Agent Card URLs for local development")
	credentialEnvAllowlist := flag.String("credential-env-allowlist", "", "comma-separated credential environment variable names tenants may reference")
	authMode := flag.String("auth-mode", "oidc", "inbound authentication mode: oidc, mtls, oidc-or-mtls, jwt-static, or development")
	jwtIssuer := flag.String("jwt-issuer", "", "required JWT issuer in jwt auth mode")
	jwtAudience := flag.String("jwt-audience", "", "required JWT audience in jwt auth mode")
	jwtPublicKeyFile := flag.String("jwt-public-key-file", "", "PEM public key file in jwt-static auth mode")
	jwtKeyID := flag.String("jwt-key-id", "", "required JWT kid mapped to the configured static public key")
	workloadIdentitiesFile := flag.String("workload-identities-file", "", "JSON mapping from SPIFFE workload IDs to tenant Principals")
	tenantTrustFile := flag.String("tenant-trust-file", "", "versioned JSON issuer-to-tenant trust policy")
	trustBundleFile := flag.String("trust-bundle-file", "", "versioned JSON OIDC issuer and SPIFFE workload trust bundle")
	trustBundleReloadInterval := flag.Duration("trust-bundle-reload-interval", 0, "optional interval for reloading the trust bundle; zero disables")
	accessPolicyFile := flag.String("access-policy-file", "", "versioned JSON local role and action authorization policy")
	tlsCertificateFile := flag.String("tls-cert-file", "", "PEM server certificate file")
	tlsKeyFile := flag.String("tls-key-file", "", "PEM server private key file")
	tlsClientCAFile := flag.String("tls-client-ca-file", "", "PEM client CA bundle for mTLS authentication")
	policyURL := flag.String("policy-url", "", "optional HTTPS external policy decision endpoint")
	policyTokenReference := flag.String("policy-token-ref", "", "optional SecretProvider reference for the policy endpoint Bearer token")
	tokenProfilesFile := flag.String("token-exchange-profiles-file", "", "JSON RFC 8693 token exchange profile map")
	remoteTimeout := flag.Duration("remote-timeout", 30*time.Second, "Agent Card and A2A request timeout")
	a2aProfiles := flag.String("a2a-profiles", "JSONRPC", "ordered A2A binding profiles: JSONRPC, HTTP_JSON, or GRPC")
	a2aGRPCCAFile := flag.String("a2a-grpc-ca-file", "", "optional PEM CA bundle for outbound A2A gRPC TLS")
	a2aGRPCServerName := flag.String("a2a-grpc-server-name", "", "optional TLS server name for outbound A2A gRPC")
	a2aCardSignatureKeyFile := flag.String("a2a-card-signature-key-file", "", "PEM public key used to verify signed AgentCards")
	a2aCardSignatureKeyID := flag.String("a2a-card-signature-key-id", "", "trusted key ID for signed AgentCards")
	a2aRequireSignedCards := flag.Bool("a2a-require-signed-cards", false, "reject AgentCards without a valid configured JWS signature")
	registryURL := flag.String("registry-url", "", "optional HTTPS Agent Registry endpoint")
	registryTokenReference := flag.String("registry-token-ref", "", "optional SecretProvider reference for the Agent Registry")
	registryCAFile := flag.String("registry-ca-file", "", "optional PEM CA bundle for the Agent Registry HTTPS endpoint")
	registryClientCertFile := flag.String("registry-client-cert-file", "", "optional PEM client certificate for the Agent Registry")
	registryClientKeyFile := flag.String("registry-client-key-file", "", "optional PEM client key for the Agent Registry")
	registryServerName := flag.String("registry-server-name", "", "optional TLS server name for the Agent Registry")
	registryMaxResponseBytes := flag.Int64("registry-max-response-bytes", 1<<20, "maximum Registry response bytes")
	registryImportTenants := flag.String("registry-import-tenants", "", "comma-separated tenant IDs to import from the external Registry")
	registrySyncInterval := flag.Duration("registry-sync-interval", 0, "optional interval for re-importing external Registry Agents; zero disables")
	gatewayURL := flag.String("gateway-url", "", "optional HTTPS A2A Gateway endpoint for outbound calls")
	gatewayTokenReference := flag.String("gateway-token-ref", "", "optional SecretProvider reference for the A2A Gateway")
	gatewayCAFile := flag.String("gateway-ca-file", "", "optional PEM CA bundle for the A2A Gateway HTTPS endpoint")
	gatewayClientCertFile := flag.String("gateway-client-cert-file", "", "optional PEM client certificate for the A2A Gateway")
	gatewayClientKeyFile := flag.String("gateway-client-key-file", "", "optional PEM client key for the A2A Gateway")
	gatewayServerName := flag.String("gateway-server-name", "", "optional TLS server name for the A2A Gateway")
	gatewayMaxResponseBytes := flag.Int64("gateway-max-response-bytes", 1<<20, "maximum Gateway response bytes")
	controlPlaneMaxAttempts := flag.Int("control-plane-max-attempts", 3, "maximum safe retry attempts for Registry reads and Gateway non-send operations")
	controlPlaneRetryInitial := flag.Duration("control-plane-retry-initial", 100*time.Millisecond, "initial control-plane retry backoff")
	controlPlaneRetryMax := flag.Duration("control-plane-retry-max", 2*time.Second, "maximum control-plane retry backoff")
	controlPlaneCircuitFailures := flag.Int("control-plane-circuit-failures", 3, "consecutive control-plane failures before opening the local circuit")
	controlPlaneCircuitOpen := flag.Duration("control-plane-circuit-open", 15*time.Second, "control-plane circuit-open duration")
	requireControlPlaneReady := flag.Bool("require-control-plane-ready", false, "fail /readyz when configured external Registry or Gateway is unavailable")
	reconcileInterval := flag.Duration("reconcile-interval", 30*time.Second, "interval for polling recoverable remote Tasks")
	workerID := flag.String("worker-id", "", "unique background worker identity; generated when empty")
	leaseDuration := flag.Duration("worker-lease-duration", 30*time.Second, "background reconciliation lease duration")
	artifactBackend := flag.String("artifact-storage", "filesystem", "Artifact object backend: filesystem or s3")
	artifactRoot := flag.String("artifact-root", "var/artifacts", "filesystem Artifact object root")
	artifactS3Endpoint := flag.String("artifact-s3-endpoint", "", "S3-compatible endpoint host and port")
	artifactS3Region := flag.String("artifact-s3-region", "", "S3 region")
	artifactS3Bucket := flag.String("artifact-s3-bucket", "", "S3 bucket")
	artifactS3Prefix := flag.String("artifact-s3-prefix", "", "S3 object prefix")
	artifactS3AccessRef := flag.String("artifact-s3-access-key-ref", "", "SecretProvider reference for S3 access key")
	artifactS3SecretRef := flag.String("artifact-s3-secret-key-ref", "", "SecretProvider reference for S3 secret key")
	artifactS3SessionRef := flag.String("artifact-s3-session-token-ref", "", "optional SecretProvider reference for S3 session token")
	artifactS3Secure := flag.Bool("artifact-s3-secure", true, "use TLS for the S3 endpoint")
	artifactMaxBytes := flag.Int64("artifact-max-bytes", 32<<20, "maximum bytes per Artifact object")
	artifactTenantMaxBytes := flag.Int64("artifact-tenant-max-bytes", 1<<30, "maximum retained Artifact bytes per tenant")
	artifactTenantMaxObjects := flag.Int64("artifact-tenant-max-objects", 10000, "maximum retained Artifact objects per tenant")
	artifactRetention := flag.Duration("artifact-retention", 30*24*time.Hour, "Artifact object retention duration")
	artifactMIMEAllowlist := flag.String("artifact-mime-allowlist", "text/*,application/json,application/pdf,application/octet-stream,application/zip,image/*,audio/*,video/*", "comma-separated detected MIME allowlist")
	artifactRequireClean := flag.Bool("artifact-require-clean", false, "require a clean malware scan before Artifact availability")
	artifactScanner := flag.String("artifact-scanner", "none", "Artifact malware scanner: none or clamav")
	artifactClamAVNetwork := flag.String("artifact-clamav-network", "tcp", "ClamAV network: tcp or unix")
	artifactClamAVAddress := flag.String("artifact-clamav-address", "", "ClamAV daemon address")
	allowPrivateArtifactURIs := flag.Bool("allow-private-artifact-urls", false, "allow private HTTPS Artifact source URLs for local development")
	rateLimitPerMinute := flag.Int("rate-limit-per-minute", 0, "authenticated requests per minute per tenant/subject/action; required outside development")
	rateLimitBurst := flag.Int("rate-limit-burst", 0, "initial burst for the authenticated request limiter")
	rateLimitBackend := flag.String("rate-limit-backend", "process", "rate-limit backend: process or postgres")
	auditFile := flag.String("audit-file", "", "0600 JSONL audit file with fsync; required outside development")
	auditURL := flag.String("audit-url", "", "optional HTTPS central audit collector endpoint")
	auditTokenReference := flag.String("audit-token-ref", "", "optional SecretProvider reference for the central audit collector")
	outboxURL := flag.String("outbox-url", "", "optional HTTPS endpoint for durable Task Event outbox delivery")
	outboxCloudEventsURL := flag.String("outbox-cloudevents-url", "", "optional HTTPS CloudEvents 1.0 collector endpoint")
	outboxCloudEventsSource := flag.String("outbox-cloudevents-source", "urn:agent-federation-hub", "CloudEvents source URI")
	outboxCloudEventsCAFile := flag.String("outbox-cloudevents-ca-file", "", "optional PEM CA bundle for the CloudEvents collector")
	outboxFile := flag.String("outbox-file", "", "optional 0600 JSONL file for local durable Task Event outbox delivery")
	outboxNATSURL := flag.String("outbox-nats-url", "", "optional NATS or TLS NATS endpoint for durable Task Event delivery")
	outboxNATSSubject := flag.String("outbox-nats-subject", "afh.task-events", "NATS subject for durable Task Event delivery")
	outboxTokenReference := flag.String("outbox-token-ref", "", "optional SecretProvider reference for the outbox endpoint Bearer token")
	outboxMaxAttempts := flag.Uint("outbox-max-attempts", 12, "maximum Outbox delivery attempts before dead-lettering; zero means unlimited")
	workerDrainTimeout := flag.Duration("worker-drain-timeout", 15*time.Second, "maximum time allowed for background workers to drain during shutdown")
	flag.Parse()

	store, err := openStore(context.Background(), *storageBackend, *journalPath, *postgresDSNEnv)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	baseSecretProvider := secrets.NewEnvProvider(parseAllowlist(*credentialEnvAllowlist))
	secretProvider, err := buildSecretProvider(baseSecretProvider, *tokenProfilesFile)
	if err != nil {
		log.Fatal(err)
	}
	configuredOutboxSinks := 0
	for _, configured := range []string{*outboxURL, *outboxCloudEventsURL, *outboxFile, *outboxNATSURL} {
		if configured != "" {
			configuredOutboxSinks++
		}
	}
	if configuredOutboxSinks > 1 ||
		(*outboxURL != "" && *outboxFile != "") ||
		(*outboxCloudEventsURL != "" && *outboxFile != "") {
		log.Fatal("--outbox-url, --outbox-cloudevents-url, --outbox-file, and --outbox-nats-url are mutually exclusive")
	}
	if *outboxTokenReference != "" && *outboxCloudEventsURL == "" && *outboxURL == "" && *outboxNATSURL == "" {
		log.Fatal("--outbox-token-ref requires --outbox-url, --outbox-cloudevents-url, or --outbox-nats-url")
	}
	var outboxPublisher worker.OutboxPublisher
	outboxMetrics := &worker.OutboxMetrics{}
	if *outboxURL != "" {
		if err := baseSecretProvider.ValidateReference(*outboxTokenReference); err != nil && *outboxTokenReference != "" {
			log.Fatalf("outbox token reference: %v", err)
		}
		var bearer func(context.Context) (string, error)
		if *outboxTokenReference != "" {
			bearer = func(ctx context.Context) (string, error) {
				return secretProvider.Resolve(ctx, *outboxTokenReference)
			}
		}
		publisher, publisherErr := worker.NewHTTPOutboxPublisher(*outboxURL, bearer)
		if publisherErr != nil {
			log.Fatalf("outbox publisher: %v", publisherErr)
		}
		publisher.Client = &http.Client{Timeout: 10 * time.Second}
		outboxPublisher = publisher
	}
	if *outboxCloudEventsURL != "" {
		if err := baseSecretProvider.ValidateReference(*outboxTokenReference); err != nil && *outboxTokenReference != "" {
			log.Fatalf("CloudEvents token reference: %v", err)
		}
		var bearer func(context.Context) (string, error)
		if *outboxTokenReference != "" {
			bearer = func(ctx context.Context) (string, error) {
				return secretProvider.Resolve(ctx, *outboxTokenReference)
			}
		}
		publisher, publisherErr := worker.NewCloudEventsPublisher(*outboxCloudEventsURL, *outboxCloudEventsSource, bearer)
		if publisherErr != nil {
			log.Fatalf("CloudEvents outbox publisher: %v", publisherErr)
		}
		if *outboxCloudEventsCAFile != "" {
			encoded, readErr := os.ReadFile(*outboxCloudEventsCAFile)
			if readErr != nil {
				log.Fatalf("read CloudEvents CA bundle: %v", readErr)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(encoded) {
				log.Fatal("CloudEvents CA bundle contains no certificates")
			}
			publisher.Client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		}
		outboxPublisher = publisher
	}
	if *outboxFile != "" {
		publisher, publisherErr := worker.NewFileOutboxPublisher(*outboxFile)
		if publisherErr != nil {
			log.Fatalf("outbox file publisher: %v", publisherErr)
		}
		defer publisher.Close()
		outboxPublisher = publisher
	}
	if *outboxNATSURL != "" {
		if err := baseSecretProvider.ValidateReference(*outboxTokenReference); err != nil && *outboxTokenReference != "" {
			log.Fatalf("NATS token reference: %v", err)
		}
		var token func(context.Context) (string, error)
		if *outboxTokenReference != "" {
			token = func(ctx context.Context) (string, error) { return secretProvider.Resolve(ctx, *outboxTokenReference) }
		}
		publisher, publisherErr := worker.NewNATSPublisher(*outboxNATSURL, *outboxNATSSubject, token)
		if publisherErr != nil {
			log.Fatalf("NATS outbox publisher: %v", publisherErr)
		}
		defer publisher.Close()
		outboxPublisher = publisher
	}
	artifactMetadata, ok := store.(core.ArtifactMetadataStore)
	if !ok {
		log.Fatal("configured Store does not implement Artifact metadata persistence")
	}
	artifacts, err := buildArtifactService(context.Background(), artifactOptions{
		Backend: *artifactBackend, Root: *artifactRoot,
		S3Endpoint: *artifactS3Endpoint, S3Region: *artifactS3Region,
		S3Bucket: *artifactS3Bucket, S3Prefix: *artifactS3Prefix,
		S3AccessKeyReference: *artifactS3AccessRef, S3SecretReference: *artifactS3SecretRef,
		S3SessionReference: *artifactS3SessionRef, S3Secure: *artifactS3Secure,
		MaxBytes: *artifactMaxBytes, TenantMaxBytes: *artifactTenantMaxBytes,
		TenantMaxObjects: *artifactTenantMaxObjects, Retention: *artifactRetention,
		MIMEAllowlist: *artifactMIMEAllowlist, RequireClean: *artifactRequireClean,
		Scanner: *artifactScanner, ClamAVNetwork: *artifactClamAVNetwork,
		ClamAVAddress: *artifactClamAVAddress, AllowPrivateURIs: *allowPrivateArtifactURIs,
	}, artifactMetadata, secretProvider)
	if err != nil {
		log.Fatal(err)
	}
	if *authMode != "development" && (!*artifactRequireClean || *artifactScanner == "none") {
		log.Fatal("non-development authentication requires --artifact-require-clean and a configured malware scanner")
	}
	limiter, closeLimiter, limiterErr := buildRateLimiter(context.Background(), *rateLimitBackend, *rateLimitPerMinute, *rateLimitBurst, os.Getenv(*postgresDSNEnv))
	if limiterErr != nil {
		log.Fatal(limiterErr)
	}
	if closeLimiter != nil {
		defer closeLimiter()
	}
	if *authMode != "development" && (limiter == nil || *rateLimitBackend != "postgres") {
		log.Fatal("non-development authentication requires --rate-limit-backend=postgres and --rate-limit-per-minute")
	}
	var auditSinks []access.AuditSink
	if *auditFile != "" {
		durableAudit, auditErr := access.OpenFileAuditSink(*auditFile)
		if auditErr != nil {
			log.Fatal(auditErr)
		}
		defer durableAudit.Close()
		auditSinks = append(auditSinks, durableAudit)
	}
	if *auditTokenReference != "" && *auditURL == "" {
		log.Fatal("--audit-token-ref requires --audit-url")
	}
	if *auditURL != "" {
		if err := baseSecretProvider.ValidateReference(*auditTokenReference); err != nil && *auditTokenReference != "" {
			log.Fatalf("audit token reference: %v", err)
		}
		var bearer func(context.Context) (string, error)
		if *auditTokenReference != "" {
			bearer = func(ctx context.Context) (string, error) {
				return secretProvider.Resolve(ctx, *auditTokenReference)
			}
		}
		centralAudit, auditErr := access.NewHTTPAuditSink(*auditURL, bearer)
		if auditErr != nil {
			log.Fatal(auditErr)
		}
		auditSinks = append(auditSinks, &access.RetryingAuditSink{Sink: centralAudit, Attempts: 3})
	}
	if *authMode != "development" && *auditFile == "" {
		log.Fatal("non-development authentication requires --audit-file")
	}
	var auditSink access.AuditSink = access.NewJSONAuditSink(os.Stderr)
	if len(auditSinks) > 0 {
		auditSink = access.FanoutAuditSink(auditSinks)
	}
	profiles, err := a2afederation.ParseBindingProfiles(*a2aProfiles)
	if err != nil {
		log.Fatalf("A2A profiles: %v", err)
	}
	grpcOptions, err := buildGRPCDialOptions(*a2aGRPCCAFile, *a2aGRPCServerName)
	if err != nil {
		log.Fatalf("A2A gRPC TLS: %v", err)
	}
	adapter, err := a2afederation.NewWithProfilesAndGRPCOptions(*remoteTimeout, profiles, grpcOptions, secretProvider)
	if err != nil {
		log.Fatalf("A2A adapter: %v", err)
	}
	if *a2aCardSignatureKeyFile != "" {
		if *a2aCardSignatureKeyID == "" {
			log.Fatal("--a2a-card-signature-key-id is required with --a2a-card-signature-key-file")
		}
		encoded, readErr := os.ReadFile(*a2aCardSignatureKeyFile)
		if readErr != nil {
			log.Fatalf("read AgentCard signature key: %v", readErr)
		}
		key, parseErr := hub.ParsePublicKeyPEM(encoded)
		if parseErr != nil {
			log.Fatalf("parse AgentCard signature key: %v", parseErr)
		}
		adapter.SetCardVerifier(a2afederation.CardVerifier{
			Required: *a2aRequireSignedCards,
			Resolver: a2afederation.StaticCardSignatureResolver{*a2aCardSignatureKeyID: key},
		})
	} else if *a2aRequireSignedCards {
		log.Fatal("--a2a-require-signed-cards requires --a2a-card-signature-key-file")
	}
	var outboundAdapter federation.Adapter = adapter
	if *gatewayURL != "" {
		if *gatewayTokenReference != "" {
			if err := baseSecretProvider.ValidateReference(*gatewayTokenReference); err != nil {
				log.Fatalf("gateway token reference: %v", err)
			}
		}
		var bearer func(context.Context) (string, error)
		if *gatewayTokenReference != "" {
			bearer = func(ctx context.Context) (string, error) { return secretProvider.Resolve(ctx, *gatewayTokenReference) }
		}
		outboundAdapter, err = gateway.NewHTTPAdapter(*gatewayURL, adapter, bearer)
		if err != nil {
			log.Fatalf("A2A Gateway: %v", err)
		}
		if *gatewayCAFile != "" {
			client, clientErr := httpClientWithTLS(*gatewayCAFile, *gatewayClientCertFile, *gatewayClientKeyFile, *gatewayServerName, 30*time.Second)
			if clientErr != nil {
				log.Fatalf("A2A Gateway CA: %v", clientErr)
			}
			outboundAdapter.(*gateway.HTTPAdapter).SetHTTPClient(client)
		} else if *gatewayClientCertFile != "" || *gatewayClientKeyFile != "" || *gatewayServerName != "" {
			client, clientErr := httpClientWithTLS("", *gatewayClientCertFile, *gatewayClientKeyFile, *gatewayServerName, 30*time.Second)
			if clientErr != nil {
				log.Fatalf("A2A Gateway TLS: %v", clientErr)
			}
			outboundAdapter.(*gateway.HTTPAdapter).SetHTTPClient(client)
		}
		gatewayClient := outboundAdapter.(*gateway.HTTPAdapter)
		gatewayClient.MaxResponseBytes = *gatewayMaxResponseBytes
		gatewayClient.SetRetryPolicy(transport.RetryPolicy{
			MaxAttempts: *controlPlaneMaxAttempts, InitialBackoff: *controlPlaneRetryInitial, MaxBackoff: *controlPlaneRetryMax,
			RetryRequest: func(request *http.Request) bool {
				return request.Method == http.MethodPost && request.Header.Get("X-AFH-Gateway-Operation") != "send"
			},
		})
		gatewayClient.SetHTTPClient(transport.WithCircuitBreaker(gatewayClient.Client, transport.CircuitBreakerPolicy{
			FailureThreshold: *controlPlaneCircuitFailures,
			OpenFor:          *controlPlaneCircuitOpen,
		}))
	}
	var externalRegistry registry.Client
	if *registryURL != "" {
		if *registryTokenReference != "" {
			if err := baseSecretProvider.ValidateReference(*registryTokenReference); err != nil {
				log.Fatalf("registry token reference: %v", err)
			}
		}
		var bearer func(context.Context) (string, error)
		if *registryTokenReference != "" {
			bearer = func(ctx context.Context) (string, error) { return secretProvider.Resolve(ctx, *registryTokenReference) }
		}
		externalRegistry, err = registry.NewHTTPClient(*registryURL, bearer)
		if err != nil {
			log.Fatalf("Agent Registry: %v", err)
		}
		if *registryCAFile != "" {
			client, clientErr := httpClientWithTLS(*registryCAFile, *registryClientCertFile, *registryClientKeyFile, *registryServerName, 10*time.Second)
			if clientErr != nil {
				log.Fatalf("Agent Registry CA: %v", clientErr)
			}
			externalRegistry.(*registry.HTTPClient).SetHTTPClient(client)
		} else if *registryClientCertFile != "" || *registryClientKeyFile != "" || *registryServerName != "" {
			client, clientErr := httpClientWithTLS("", *registryClientCertFile, *registryClientKeyFile, *registryServerName, 10*time.Second)
			if clientErr != nil {
				log.Fatalf("Agent Registry TLS: %v", clientErr)
			}
			externalRegistry.(*registry.HTTPClient).SetHTTPClient(client)
		}
		registryClient := externalRegistry.(*registry.HTTPClient)
		registryClient.MaxResponseBytes = *registryMaxResponseBytes
		registryClient.SetRetryPolicy(transport.RetryPolicy{
			MaxAttempts: *controlPlaneMaxAttempts, InitialBackoff: *controlPlaneRetryInitial, MaxBackoff: *controlPlaneRetryMax,
		})
		registryClient.SetHTTPClient(transport.WithCircuitBreaker(registryClient.Client, transport.CircuitBreakerPolicy{
			FailureThreshold: *controlPlaneCircuitFailures,
			OpenFor:          *controlPlaneCircuitOpen,
		}))
		if err := externalRegistry.Health(context.Background()); err != nil {
			log.Fatalf("Agent Registry health: %v", err)
		}
	}
	service := &hub.Service{
		Store: store, Adapter: outboundAdapter, PublicBaseURL: *publicBaseURL,
		AllowPrivateAgentURLs: *allowPrivateAgentURLs,
		Secrets:               secretProvider,
		Artifacts:             artifacts,
	}
	configuredTenants := make(map[string]struct{})
	if *agentConfigPath != "" {
		configuredAgents, configErr := agentconfig.LoadFile(*agentConfigPath)
		if configErr != nil {
			if !errors.Is(configErr, os.ErrNotExist) {
				log.Fatal(configErr)
			}
			log.Printf("Agent configuration %q does not exist; continuing without configured Agents", *agentConfigPath)
		} else {
			for _, registration := range configuredAgents.EnabledAgents() {
				configuredTenants[registration.TenantID] = struct{}{}
				if registration.AllowsPrivateURLs(configuredAgents.Defaults) && !*allowPrivateAgentURLs {
					log.Fatalf("configured Agent %q allows private URLs; pass --allow-private-agent-urls for local development", registration.ID)
				}
				registered, registerErr := service.RegisterAgentWithPolicy(
					context.Background(), registration.TenantID,
					hub.RegisterAgentInput{ID: registration.ID, CardURL: registration.CardURL, CredentialEnv: registration.CredentialEnv},
					registration.RegistrationPolicy(configuredAgents.Defaults),
				)
				if registerErr != nil {
					log.Fatalf("register configured Agent %q: %v", registration.ID, registerErr)
				}
				if externalRegistry != nil {
					if err := externalRegistry.Register(context.Background(), registered); err != nil {
						log.Fatalf("publish Agent %q to external Registry: %v", registration.ID, err)
					}
				}
				log.Printf("registered configured Agent %q (%s)", registered.ID, registered.Name)
			}
		}
	}
	if externalRegistry != nil {
		for _, tenantID := range registryTenants(*registryImportTenants, configuredTenants) {
			if err := importRegistryAgents(context.Background(), externalRegistry, service, tenantID, *registryURL); err != nil {
				log.Printf("external Registry import for tenant %q failed; keeping local cache: %v", tenantID, err)
			}
		}
	}
	revocations, ok := store.(core.RevocationStore)
	if !ok {
		log.Fatal("configured Store does not implement token revocation")
	}
	var trustBundleManager *hub.TrustBundleManager
	if *trustBundleFile != "" {
		if *tenantTrustFile != "" || *workloadIdentitiesFile != "" {
			log.Fatal("--trust-bundle-file cannot be combined with --tenant-trust-file or --workload-identities-file")
		}
		trustBundleManager, err = hub.NewTrustBundleManager(*trustBundleFile)
		if err != nil {
			log.Fatalf("trust bundle: %v", err)
		}
		log.Printf("loaded trust bundle generation %d from %s", trustBundleManagerGeneration(trustBundleManager), *trustBundleFile)
	}
	security := securityOptions{
		AuthMode: *authMode, Issuer: *jwtIssuer, Audience: *jwtAudience,
		PublicKeyFile: *jwtPublicKeyFile, KeyID: *jwtKeyID,
		WorkloadIdentityFile: *workloadIdentitiesFile,
		PolicyURL:            *policyURL, PolicyTokenReference: *policyTokenReference,
		TokenProfilesFile: *tokenProfilesFile, TLSClientCAFile: *tlsClientCAFile,
		TenantTrustFile: *tenantTrustFile, TrustBundleFile: *trustBundleFile, TrustBundle: trustBundleManager,
		RequireTenantTrust: *authMode != "development",
		AccessPolicyFile:   *accessPolicyFile,
	}
	authenticator, err := buildAuthenticator(context.Background(), security, revocations)
	if err != nil {
		log.Fatal(err)
	}
	authorizer, err := buildAuthorizer(security, secretProvider)
	if err != nil {
		log.Fatal(err)
	}
	tlsConfiguration, err := buildTLSConfig(*authMode, *tlsClientCAFile)
	if err != nil {
		log.Fatal(err)
	}
	if *authMode != "development" && (*tlsCertificateFile == "" || *tlsKeyFile == "") {
		log.Fatal("non-development authentication requires --tls-cert-file and --tls-key-file")
	}
	if (*tlsCertificateFile == "") != (*tlsKeyFile == "") {
		log.Fatal("TLS certificate and key files must be configured together")
	}
	readiness := func(ctx context.Context) error {
		healthStore, ok := store.(core.HealthStore)
		if !ok {
			return errors.New("configured store does not expose a health check")
		}
		if err := healthStore.Health(ctx); err != nil {
			return err
		}
		if trustBundleManager != nil {
			if err := trustBundleManager.ValidateCurrent(); err != nil {
				return fmt.Errorf("trust bundle is not ready: %w", err)
			}
		}
		if objectHealth, ok := artifacts.Objects.(artifactstore.HealthStore); ok {
			if err := objectHealth.Health(ctx); err != nil {
				return err
			}
		}
		if *requireControlPlaneReady {
			if externalRegistry != nil {
				if err := externalRegistry.Health(ctx); err != nil {
					return fmt.Errorf("external Registry is not ready: %w", err)
				}
			}
			if gatewayClient, ok := outboundAdapter.(*gateway.HTTPAdapter); ok {
				if err := gatewayClient.Health(ctx); err != nil {
					return fmt.Errorf("external Gateway is not ready: %w", err)
				}
			}
		}
		return nil
	}
	handler := (&hub.HTTPHandler{
		Service: service, DecodePush: a2afederation.DecodePush,
		Authenticator: authenticator, Authorizer: authorizer,
		Audit: auditSink, Limiter: limiter, Metrics: outboxMetrics.Prometheus,
		Readiness: readiness,
	}).Handler()
	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         tlsConfiguration,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if trustBundleManager != nil && *trustBundleReloadInterval > 0 {
		go trustBundleManager.Watch(ctx, *trustBundleReloadInterval, func(reloadErr error) {
			log.Printf("trust bundle reload failed; retaining last valid snapshot: %v", reloadErr)
		})
	}
	resolvedWorkerID := *workerID
	if resolvedWorkerID == "" {
		resolvedWorkerID = "hub-" + core.NewID()
	}
	background := &worker.Reconciler{
		Store: store, Tasks: service, WorkerID: resolvedWorkerID,
		LeaseDuration: *leaseDuration, PollInterval: *reconcileInterval,
	}
	var registrySyncCancel context.CancelFunc
	if externalRegistry != nil && *registrySyncInterval > 0 {
		registryCtx, cancel := context.WithCancel(ctx)
		registrySyncCancel = cancel
		go func() {
			ticker := time.NewTicker(*registrySyncInterval)
			defer ticker.Stop()
			for {
				select {
				case <-registryCtx.Done():
					return
				case <-ticker.C:
					for _, tenantID := range registryTenants(*registryImportTenants, configuredTenants) {
						if err := importRegistryAgents(registryCtx, externalRegistry, service, tenantID, *registryURL); err != nil {
							log.Printf("external Registry sync for tenant %q failed; retaining local cache: %v", tenantID, err)
						}
					}
				}
			}
		}()
	}
	inbox := &worker.InboxProcessor{
		Store: store, Apply: service, WorkerID: resolvedWorkerID + ":inbox",
		LeaseDuration: *leaseDuration, PollInterval: time.Second,
	}
	artifactLifecycle := &artifactstore.Lifecycle{
		Metadata: artifactMetadata, Objects: artifacts.Objects,
		WorkerID: resolvedWorkerID + ":artifacts", LeaseDuration: *leaseDuration,
		PollInterval: time.Minute,
	}
	var outbox *worker.OutboxProcessor
	if outboxPublisher != nil {
		outbox = &worker.OutboxProcessor{
			Store: store, Publisher: outboxPublisher, WorkerID: resolvedWorkerID + ":outbox",
			LeaseDuration: *leaseDuration, PollInterval: time.Second,
			MaxAttempts: uint32(*outboxMaxAttempts),
			Metrics:     outboxMetrics,
		}
	}
	var workers sync.WaitGroup
	runWorker := func(name string, run func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := run(); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("%s stopped: %v", name, err)
			}
		}()
	}
	runWorker("background reconciliation", func() error { return background.Run(ctx) })
	runWorker("Push inbox processor", func() error { return inbox.Run(ctx) })
	runWorker("Artifact lifecycle worker", func() error { return artifactLifecycle.Run(ctx) })
	if outbox != nil {
		runWorker("outbox publisher", func() error { return outbox.Run(ctx) })
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}()

	log.Printf("Agent Federation Hub listening on %s", *listen)
	var serveErr error
	if *tlsCertificateFile != "" {
		serveErr = server.ListenAndServeTLS(*tlsCertificateFile, *tlsKeyFile)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatal(serveErr)
	}
	stop()
	if registrySyncCancel != nil {
		registrySyncCancel()
	}
	drainDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(drainDone)
	}()
	drainTimer := time.NewTimer(*workerDrainTimeout)
	defer drainTimer.Stop()
	select {
	case <-drainDone:
		log.Printf("background workers drained")
	case <-drainTimer.C:
		log.Printf("background worker drain timed out after %s", *workerDrainTimeout)
	}
}

func httpClientWithTLS(caFile, clientCertFile, clientKeyFile, serverName string, timeout time.Duration) (*http.Client, error) {
	var caPEM, certificatePEM, keyPEM []byte
	var err error
	if caFile != "" {
		caPEM, err = os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle: %w", err)
		}
	}
	if clientCertFile != "" {
		certificatePEM, err = os.ReadFile(clientCertFile)
		if err != nil {
			return nil, fmt.Errorf("read client certificate: %w", err)
		}
	}
	if clientKeyFile != "" {
		keyPEM, err = os.ReadFile(clientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read client key: %w", err)
		}
	}
	options, err := transport.LoadTLSOptions(caPEM, certificatePEM, keyPEM, serverName)
	if err != nil {
		return nil, err
	}
	return transport.NewHTTPClient(timeout, options), nil
}

func registryTenants(explicit string, configured map[string]struct{}) []string {
	seen := make(map[string]struct{})
	var tenants []string
	for _, value := range strings.Split(explicit, ",") {
		if tenant := strings.TrimSpace(value); tenant != "" {
			seen[tenant] = struct{}{}
		}
	}
	if len(seen) == 0 {
		for tenant := range configured {
			seen[tenant] = struct{}{}
		}
	}
	for tenant := range seen {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	return tenants
}

func importRegistryAgents(ctx context.Context, external registry.Client, service *hub.Service, tenantID, registryEndpoint string) error {
	if strings.TrimSpace(tenantID) == "" {
		return errors.New("tenant ID is required for Registry import")
	}
	agents, err := external.List(ctx, tenantID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(agents))
	for _, candidate := range agents {
		if candidate.TenantID != "" && candidate.TenantID != tenantID {
			continue
		}
		if strings.TrimSpace(candidate.ID) == "" {
			return errors.New("external Registry returned an Agent without an ID")
		}
		seen[candidate.ID] = struct{}{}
		if _, err := service.RegisterAgent(ctx, tenantID, hub.RegisterAgentInput{
			ID: candidate.ID, CardURL: candidate.CardURL, CredentialEnv: candidate.CredentialEnv,
			RegistrationSource: "external-registry", RegistryEndpoint: registryEndpoint,
		}); err != nil {
			return fmt.Errorf("import Agent %q: %w", candidate.ID, err)
		}
	}
	local, err := service.ListAgents(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("list local Agent cache: %w", err)
	}
	now := time.Now().UTC()
	for _, candidate := range local {
		if candidate.RegistrationSource != "external-registry" || candidate.RegistryEndpoint != registryEndpoint {
			continue
		}
		candidate.LastRegistrySyncAt = &now
		if _, exists := seen[candidate.ID]; !exists {
			candidate.HealthStatus = core.AgentHealthStale
			candidate.HealthMessage = "Agent was not present in the latest Registry snapshot"
		} else if candidate.HealthStatus == core.AgentHealthStale {
			candidate.HealthStatus = core.AgentHealthHealthy
			candidate.HealthMessage = ""
		}
		candidate.UpdatedAt = now
		if err := service.Store.PutAgent(ctx, candidate); err != nil {
			return fmt.Errorf("update Registry cache Agent %q: %w", candidate.ID, err)
		}
	}
	return nil
}

func buildRateLimiter(ctx context.Context, backend string, perMinute, burst int, postgresDSN string) (access.RateLimiter, func(), error) {
	switch backend {
	case "process":
		return access.NewTokenBucketLimiter(perMinute, burst), nil, nil
	case "postgres":
		limiter, err := access.OpenPostgresRateLimiter(ctx, postgresDSN, perMinute, burst)
		if err != nil {
			return nil, nil, err
		}
		return limiter, limiter.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported rate-limit backend %q", backend)
	}
}

func openStore(ctx context.Context, backend, journalPath, postgresDSNEnv string) (core.DurableStore, error) {
	switch backend {
	case "journal":
		return core.OpenJournal(journalPath)
	case "postgres":
		dataSourceName := os.Getenv(postgresDSNEnv)
		if dataSourceName == "" {
			return nil, fmt.Errorf("PostgreSQL DSN environment variable %q is empty", postgresDSNEnv)
		}
		return core.OpenPostgres(ctx, dataSourceName)
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", backend)
	}
}

func parseAllowlist(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		if name := strings.TrimSpace(item); name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func trustBundleManagerGeneration(manager *hub.TrustBundleManager) uint64 {
	if manager == nil {
		return 0
	}
	generation, ok := manager.Generation()
	if !ok {
		return 0
	}
	return generation
}

func buildGRPCDialOptions(caFile, serverName string) ([]grpc.DialOption, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(serverName)}
	if caFile != "" {
		encoded, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read gRPC CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(encoded) {
			return nil, errors.New("gRPC CA bundle contains no certificates")
		}
		config.RootCAs = pool
	}
	return []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(config))}, nil
}
