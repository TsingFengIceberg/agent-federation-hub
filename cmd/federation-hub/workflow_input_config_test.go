package main

import (
	"context"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

func TestBuildWorkflowInputStoreRequiresEncryptedFileInProduction(t *testing.T) {
	provider := secrets.NewEnvProviderForTest(map[string]string{"WORKFLOW_KEY": "01234567890123456789012345678901"})
	if _, err := buildWorkflowInputStore(context.Background(), workflowInputOptions{Backend: "memory"}, provider, true); err == nil {
		t.Fatal("production accepted memory workflow input store")
	}
	store, err := buildWorkflowInputStore(context.Background(), workflowInputOptions{
		Backend: "file", Root: t.TempDir(), KeyReference: "WORKFLOW_KEY", KeyID: "v1",
	}, provider, true)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("file workflow input store is nil")
	}
}

func TestBuildWorkflowInputStoreKeepsMemoryForDevelopment(t *testing.T) {
	store, err := buildWorkflowInputStore(context.Background(), workflowInputOptions{Backend: "memory"}, nil, false)
	if err != nil || store == nil {
		t.Fatalf("development memory store: %v", err)
	}
}
