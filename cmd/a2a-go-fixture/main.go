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
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/interop"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:4101", "HTTP listen address")
	publicURL := flag.String("public-url", "http://127.0.0.1:4101", "base URL advertised by the Agent Card")
	flag.Parse()

	card := fixtureCard(*publicURL)
	handler := a2asrv.NewHandler(
		interop.ScenarioExecutor{},
		a2asrv.WithCapabilityChecks(&card.Capabilities),
	)

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/a2a", a2asrv.NewJSONRPCHandler(handler))

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown failed: %v", err)
		}
	}()

	log.Printf("Go A2A fixture listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func fixtureCard(publicURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Agent Federation Hub Go fixture",
		Description: "Deterministic black-box A2A interoperability fixture",
		Version:     "0.1.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(fmt.Sprintf("%s/a2a", publicURL), a2a.TransportProtocolJSONRPC),
		},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain", "application/json", "application/octet-stream"},
		Skills: []a2a.AgentSkill{
			{
				ID:          "interop-scenarios",
				Name:        "Interoperability scenarios",
				Description: "Runs deterministic Message, Task, input, streaming, and cancellation scenarios",
				Tags:        []string{"interop", "test"},
				Examples:    []string{"message", "task", "input-required", "long-running"},
			},
		},
	}
}
