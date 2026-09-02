package a2afederation

import (
	"context"
	"errors"
	"fmt"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// ExtensionHandler validates and activates one provider-defined A2A
// extension. The Hub never interprets extension payloads itself; a domain or
// protocol package registers the semantics explicitly.
type ExtensionHandler interface {
	Validate(context.Context, core.Agent, map[string]any) error
}

// ExtensionPolicy controls the boundary between extension admission and
// activation. Strict mode is opt-in because a provider may advertise an
// extension whose semantics are intentionally opaque to this Hub deployment.
type ExtensionPolicy struct {
	Handlers       map[string]ExtensionHandler
	RequireHandler bool
}

func (p ExtensionPolicy) Validate(ctx context.Context, agent core.Agent, extensions []string, metadata map[string]any) error {
	if len(extensions) == 0 {
		return nil
	}
	declared := make(map[string]struct{}, len(agent.Extensions))
	for _, uri := range agent.Extensions {
		declared[uri] = struct{}{}
	}
	for _, uri := range extensions {
		if _, ok := declared[uri]; !ok {
			return fmt.Errorf("AgentCard does not advertise requested extension %q", uri)
		}
		handler, ok := p.Handlers[uri]
		if !ok {
			if p.RequireHandler {
				return fmt.Errorf("A2A extension %q has no activated handler", uri)
			}
			continue
		}
		if handler == nil {
			return errors.New("A2A extension handler is nil")
		}
		if err := handler.Validate(ctx, agent, metadata); err != nil {
			return fmt.Errorf("validate A2A extension %q: %w", uri, err)
		}
	}
	return nil
}
