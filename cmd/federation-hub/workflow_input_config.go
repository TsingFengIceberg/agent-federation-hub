package main

import (
	"context"
	"errors"
	"fmt"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/orchestration"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

type workflowInputOptions struct {
	Backend      string
	Root         string
	KeyReference string
	KeyID        string
}

func buildWorkflowInputStore(ctx context.Context, options workflowInputOptions, provider secrets.Provider, production bool) (orchestration.WorkflowInputStore, error) {
	switch options.Backend {
	case "memory":
		if production {
			return nil, errors.New("non-development authentication requires --workflow-input-storage=file")
		}
		return orchestration.NewMemoryInputStore(), nil
	case "file":
		if provider == nil {
			return nil, errors.New("workflow input vault requires a SecretProvider")
		}
		if options.KeyReference == "" || options.KeyID == "" {
			return nil, errors.New("file workflow input vault requires key reference and key ID")
		}
		if err := provider.ValidateReference(options.KeyReference); err != nil {
			return nil, fmt.Errorf("workflow input key reference: %w", err)
		}
		value, err := provider.Resolve(ctx, options.KeyReference)
		if err != nil || value == "" {
			return nil, errors.New("workflow input encryption key is unavailable")
		}
		store, err := orchestration.NewFileInputStore(options.Root, artifactstore.StaticKeyProvider{KeyID: options.KeyID, Key: []byte(value)})
		if err != nil {
			return nil, err
		}
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported workflow input storage %q", options.Backend)
	}
}
