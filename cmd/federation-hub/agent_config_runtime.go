package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/agentconfig"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/registry"
)

// reconcileConfiguredAgents applies a complete, already schema-validated
// configuration. All remote Cards are preflighted before any local Agent is
// written. Removed entries are retained as STALE records so existing Tasks
// keep their historical Agent association and can be audited safely.
func reconcileConfiguredAgents(
	ctx context.Context,
	service *hub.Service,
	externalRegistry registry.Client,
	config agentconfig.File,
	configuredTenants map[string]struct{},
	configuredTenantsMu *sync.RWMutex,
	managed map[string]struct{},
	allowPrivateURLs bool,
	first bool,
) error {
	enabled := config.EnabledAgents()
	type candidate struct {
		registration agentconfig.Registration
		policy       hub.AgentRegistrationPolicy
		input        hub.RegisterAgentInput
	}
	candidates := make([]candidate, 0, len(enabled))
	for _, registration := range enabled {
		if registration.AllowsPrivateURLs(config.Defaults) && !allowPrivateURLs {
			return fmt.Errorf("configured Agent %q allows private URLs; pass --allow-private-agent-urls for local development", registration.ID)
		}
		entry := candidate{
			registration: registration,
			policy:       registration.RegistrationPolicy(config.Defaults),
			input: hub.RegisterAgentInput{
				ID: registration.ID, CardURL: registration.CardURL, CredentialEnv: registration.CredentialEnv,
				RegistrationSource: "agent-config",
			},
		}
		if _, err := service.ValidateAgentRegistration(ctx, registration.TenantID, entry.input, entry.policy); err != nil {
			return fmt.Errorf("preflight configured Agent %q: %w", registration.ID, err)
		}
		candidates = append(candidates, entry)
	}

	desired := make(map[string]struct{}, len(candidates))
	for _, entry := range candidates {
		registration := entry.registration
		key := configAgentKey(registration.TenantID, registration.ID)
		desired[key] = struct{}{}
		registered, err := service.RegisterAgentWithPolicy(ctx, registration.TenantID, entry.input, entry.policy)
		if err != nil {
			// The Card can change between preflight and application. Treat that as
			// a failed candidate and leave the runtime snapshot unchanged.
			return fmt.Errorf("register configured Agent %q: %w", registration.ID, err)
		}
		if configuredTenantsMu != nil {
			configuredTenantsMu.Lock()
		}
		configuredTenants[registration.TenantID] = struct{}{}
		if configuredTenantsMu != nil {
			configuredTenantsMu.Unlock()
		}
		if externalRegistry != nil {
			if err := externalRegistry.Register(ctx, registered); err != nil {
				// External Registry publication is a separate control-plane sink.
				// Local admission remains valid and the next sync can retry it.
				log.Printf("publish Agent %q to external Registry failed; retaining local registration: %v", registration.ID, err)
			}
		}
		if first {
			log.Printf("registered configured Agent %q (%s)", registered.ID, registered.Name)
		} else {
			log.Printf("reconciled configured Agent %q (%s)", registered.ID, registered.Name)
		}
	}

	for key := range managed {
		if _, exists := desired[key]; exists {
			continue
		}
		tenantID, agentID, ok := splitConfigAgentKey(key)
		if !ok {
			continue
		}
		agent, err := service.Store.GetAgent(ctx, tenantID, agentID)
		if err != nil {
			continue
		}
		now := timeNowUTC()
		agent.HealthStatus = core.AgentHealthStale
		agent.HealthMessage = "Agent is disabled or absent from the accepted Agent configuration"
		agent.LastHealthCheckAt = &now
		agent.UpdatedAt = now
		if err := service.Store.PutAgent(ctx, agent); err != nil {
			return fmt.Errorf("mark removed Agent %q stale: %w", agentID, err)
		}
		log.Printf("marked removed configured Agent %q stale", agentID)
	}

	if configuredTenantsMu != nil {
		configuredTenantsMu.Lock()
		defer configuredTenantsMu.Unlock()
	}
	for tenant := range configuredTenants {
		if !tenantInConfig(config, tenant) {
			delete(configuredTenants, tenant)
		}
	}
	for key := range managed {
		delete(managed, key)
	}
	for key := range desired {
		managed[key] = struct{}{}
	}
	return nil
}

func configAgentKey(tenantID, agentID string) string {
	return tenantID + "\x00" + agentID
}

func splitConfigAgentKey(value string) (string, string, bool) {
	parts := strings.SplitN(value, "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func tenantInConfig(config agentconfig.File, tenantID string) bool {
	for _, registration := range config.EnabledAgents() {
		if registration.TenantID == tenantID {
			return true
		}
	}
	return false
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
