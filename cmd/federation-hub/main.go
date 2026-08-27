package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	journalPath := flag.String("journal", "var/hub.journal", "append-only Hub journal path")
	publicBaseURL := flag.String("public-base-url", "", "public HTTPS base URL used for optional A2A Push callbacks")
	allowPrivateAgentURLs := flag.Bool("allow-private-agent-urls", false, "allow HTTP or private Agent Card URLs for local development")
	credentialEnvAllowlist := flag.String("credential-env-allowlist", "", "comma-separated credential environment variable names tenants may reference")
	remoteTimeout := flag.Duration("remote-timeout", 30*time.Second, "Agent Card and A2A request timeout")
	reconcileInterval := flag.Duration("reconcile-interval", 30*time.Second, "interval for polling recoverable remote Tasks")
	flag.Parse()

	store, err := core.OpenJournal(*journalPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	adapter := a2afederation.New(*remoteTimeout)
	service := &hub.Service{
		Store: store, Adapter: adapter, PublicBaseURL: *publicBaseURL,
		AllowPrivateAgentURLs: *allowPrivateAgentURLs,
		AllowedCredentialEnv:  parseAllowlist(*credentialEnvAllowlist),
	}
	handler := (&hub.HTTPHandler{Service: service, DecodePush: a2afederation.DecodePush}).Handler()
	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go reconcileLoop(ctx, service, *reconcileInterval)
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

func parseAllowlist(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		if name := strings.TrimSpace(item); name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func reconcileLoop(ctx context.Context, service *hub.Service, interval time.Duration) {
	reconcile := func() {
		if err := service.Recover(ctx, false); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("task reconciliation: %v", err)
		}
	}
	reconcile()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
