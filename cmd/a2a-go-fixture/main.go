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

	"github.com/TsingFengIceberg/agent-federation-hub/internal/interop"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	a2apush "github.com/a2aproject/a2a-go/v2/a2asrv/push"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:4101", "HTTP listen address")
	publicURL := flag.String("public-url", "http://127.0.0.1:4101", "base URL advertised by the Agent Card")
	pushNotifications := flag.Bool("push", false, "enable the A2A HTTP Push sender and advertise Push support")
	name := flag.String("name", "Agent Federation Hub Go fixture", "Agent Card name")
	description := flag.String("description", "Deterministic black-box A2A interoperability fixture", "Agent Card description")
	skills := flag.String("skills", "interop-scenarios", "comma-separated AgentCard skill IDs")
	flag.Parse()

	card := fixtureCard(*publicURL, *name, *description, splitSkills(*skills))
	options := []a2asrv.RequestHandlerOption{a2asrv.WithCapabilityChecks(&card.Capabilities)}
	if *pushNotifications {
		card.Capabilities.PushNotifications = true
		options = append(options, a2asrv.WithPushNotifications(
			a2apush.NewInMemoryStore(),
			a2apush.NewHTTPPushSender(&a2apush.HTTPSenderConfig{
				AllowPrivateNetworks: true,
				FailOnError:          true,
			}),
		))
	}
	handler := a2asrv.NewHandler(interop.ScenarioExecutor{}, options...)

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

func fixtureCard(publicURL, name, description string, skillIDs []string) *a2a.AgentCard {
	if len(skillIDs) == 0 {
		skillIDs = []string{"interop-scenarios"}
	}
	cardSkills := make([]a2a.AgentSkill, 0, len(skillIDs))
	for _, id := range skillIDs {
		cardSkills = append(cardSkills, a2a.AgentSkill{
			ID: id, Name: id, Description: "Deterministic provider skill for " + id,
			Tags: []string{"fixture", id}, Examples: []string{"artifact-data", "artifact-file", "input-required"},
		})
	}
	return &a2a.AgentCard{
		Name:        name,
		Description: description,
		Version:     "0.1.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(fmt.Sprintf("%s/a2a", publicURL), a2a.TransportProtocolJSONRPC),
		},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain", "application/json", "application/octet-stream"},
		Skills:             cardSkills,
	}
}

func splitSkills(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
