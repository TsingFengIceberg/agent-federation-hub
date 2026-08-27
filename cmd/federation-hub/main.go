package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/access"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/worker"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	journalPath := flag.String("journal", "var/hub.journal", "append-only Hub journal path")
	storageBackend := flag.String("storage", "journal", "storage backend: journal or postgres")
	postgresDSNEnv := flag.String("postgres-dsn-env", "AFH_POSTGRES_DSN", "environment variable containing the PostgreSQL DSN")
	publicBaseURL := flag.String("public-base-url", "", "public HTTPS base URL used for optional A2A Push callbacks")
	allowPrivateAgentURLs := flag.Bool("allow-private-agent-urls", false, "allow HTTP or private Agent Card URLs for local development")
	credentialEnvAllowlist := flag.String("credential-env-allowlist", "", "comma-separated credential environment variable names tenants may reference")
	authMode := flag.String("auth-mode", "jwt", "inbound authentication mode: jwt or development")
	jwtIssuer := flag.String("jwt-issuer", "", "required JWT issuer in jwt auth mode")
	jwtAudience := flag.String("jwt-audience", "", "required JWT audience in jwt auth mode")
	jwtPublicKeyFile := flag.String("jwt-public-key-file", "", "PEM public key file in jwt auth mode")
	jwtKeyID := flag.String("jwt-key-id", "", "required JWT kid mapped to the configured public key")
	remoteTimeout := flag.Duration("remote-timeout", 30*time.Second, "Agent Card and A2A request timeout")
	reconcileInterval := flag.Duration("reconcile-interval", 30*time.Second, "interval for polling recoverable remote Tasks")
	workerID := flag.String("worker-id", "", "unique background worker identity; generated when empty")
	leaseDuration := flag.Duration("worker-lease-duration", 30*time.Second, "background reconciliation lease duration")
	flag.Parse()

	store, err := openStore(context.Background(), *storageBackend, *journalPath, *postgresDSNEnv)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	secretProvider := secrets.NewEnvProvider(parseAllowlist(*credentialEnvAllowlist))
	adapter := a2afederation.New(*remoteTimeout, secretProvider)
	service := &hub.Service{
		Store: store, Adapter: adapter, PublicBaseURL: *publicBaseURL,
		AllowPrivateAgentURLs: *allowPrivateAgentURLs,
		Secrets:               secretProvider,
	}
	authenticator, err := buildAuthenticator(*authMode, *jwtIssuer, *jwtAudience, *jwtPublicKeyFile, *jwtKeyID)
	if err != nil {
		log.Fatal(err)
	}
	handler := (&hub.HTTPHandler{
		Service: service, DecodePush: a2afederation.DecodePush,
		Authenticator: authenticator, Authorizer: access.DefaultScopeAuthorizer(),
		Audit: access.NewJSONAuditSink(os.Stderr),
	}).Handler()
	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	resolvedWorkerID := *workerID
	if resolvedWorkerID == "" {
		resolvedWorkerID = "hub-" + core.NewID()
	}
	background := &worker.Reconciler{
		Store: store, Tasks: service, WorkerID: resolvedWorkerID,
		LeaseDuration: *leaseDuration, PollInterval: *reconcileInterval,
	}
	inbox := &worker.InboxProcessor{
		Store: store, Apply: service, WorkerID: resolvedWorkerID + ":inbox",
		LeaseDuration: *leaseDuration, PollInterval: time.Second,
	}
	go func() {
		if err := background.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("background reconciliation stopped: %v", err)
		}
	}()
	go func() {
		if err := inbox.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Push inbox processor stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}()

	log.Printf("Agent Federation Hub listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
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

func buildAuthenticator(mode, issuer, audience, publicKeyFile, keyID string) (hub.Authenticator, error) {
	switch mode {
	case "development":
		log.Print("WARNING: development header authentication is enabled and is not a production security boundary")
		return hub.DevelopmentAuthenticator{}, nil
	case "jwt":
		if issuer == "" || audience == "" || publicKeyFile == "" || keyID == "" {
			return nil, errors.New("jwt auth mode requires --jwt-issuer, --jwt-audience, --jwt-public-key-file, and --jwt-key-id")
		}
		encoded, err := os.ReadFile(publicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read JWT public key: %w", err)
		}
		key, err := hub.ParsePublicKeyPEM(encoded)
		if err != nil {
			return nil, err
		}
		return &hub.JWTAuthenticator{
			Issuer: issuer, Audience: audience,
			Keys: hub.StaticKeyProvider{Keys: map[string]any{keyID: key}},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", mode)
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
