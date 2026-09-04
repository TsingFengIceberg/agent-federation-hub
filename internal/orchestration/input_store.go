package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// WorkflowInputStore is a replaceable vault for provider input. Workflow
// aggregates persist only InputRef and InputDigest; implementations should use
// encryption/KMS and tenant access controls in production.
type WorkflowInputStore interface {
	Put(context.Context, string, string, string, WorkflowInput) (string, error)
	Get(context.Context, string, string) (WorkflowInput, error)
}

// WorkflowInput is encrypted outside the Workflow aggregate so provider input
// remains recoverable without exposing text or structured input in Task/Event
// journals. It deliberately mirrors the public Task input contract.
type WorkflowInput struct {
	Text  string      `json:"text,omitempty"`
	Parts []core.Part `json:"parts,omitempty"`
}

func (input WorkflowInput) validate() error {
	if input.Text == "" && len(input.Parts) == 0 {
		return errors.New("workflow provider input requires text or at least one part")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("workflow provider input must be JSON: %w", err)
	}
	if len(encoded) > 1<<20 {
		return errors.New("workflow provider input exceeds 1 MiB")
	}
	return nil
}

// MemoryInputStore is a process-local fixture. It deliberately does not claim
// restart durability; production deployments must inject a durable encrypted
// implementation.
type MemoryInputStore struct {
	mu     sync.RWMutex
	values map[string]WorkflowInput
}

func NewMemoryInputStore() *MemoryInputStore {
	return &MemoryInputStore{values: make(map[string]WorkflowInput)}
}

func (s *MemoryInputStore) Put(_ context.Context, tenantID, workflowID, stepID string, input WorkflowInput) (string, error) {
	if s == nil || tenantID == "" || workflowID == "" || stepID == "" {
		return "", errors.New("workflow input store requires tenant, workflow, step, and input")
	}
	if err := input.validate(); err != nil {
		return "", err
	}
	ref := fmt.Sprintf("%s/%s/%s", tenantID, workflowID, stepID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.values[ref]; ok && !workflowInputsEqual(existing, input) {
		return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
	}
	s.values[ref] = cloneWorkflowInput(input)
	return ref, nil
}

func (s *MemoryInputStore) Get(_ context.Context, tenantID, ref string) (WorkflowInput, error) {
	if s == nil || tenantID == "" || ref == "" {
		return WorkflowInput{}, errors.New("workflow input lookup requires tenant and reference")
	}
	if len(ref) <= len(tenantID) || ref[:len(tenantID)] != tenantID || ref[len(tenantID)] != '/' {
		return WorkflowInput{}, core.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[ref]
	if !ok {
		return WorkflowInput{}, core.ErrNotFound
	}
	return cloneWorkflowInput(value), nil
}

func cloneWorkflowInput(input WorkflowInput) WorkflowInput {
	encoded, err := json.Marshal(input)
	if err != nil {
		return WorkflowInput{}
	}
	var clone WorkflowInput
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

func workflowInputsEqual(first, second WorkflowInput) bool {
	firstEncoded, firstErr := json.Marshal(first)
	secondEncoded, secondErr := json.Marshal(second)
	return firstErr == nil && secondErr == nil && string(firstEncoded) == string(secondEncoded)
}
