package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// WorkflowInputStore is a replaceable vault for provider input. Workflow
// aggregates persist only InputRef and InputDigest; implementations should use
// encryption/KMS and tenant access controls in production.
type WorkflowInputStore interface {
	Put(context.Context, string, string, string, string) (string, error)
	Get(context.Context, string, string) (string, error)
}

// MemoryInputStore is a process-local fixture. It deliberately does not claim
// restart durability; production deployments must inject a durable encrypted
// implementation.
type MemoryInputStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewMemoryInputStore() *MemoryInputStore {
	return &MemoryInputStore{values: make(map[string]string)}
}

func (s *MemoryInputStore) Put(_ context.Context, tenantID, workflowID, stepID, text string) (string, error) {
	if s == nil || tenantID == "" || workflowID == "" || stepID == "" || text == "" {
		return "", errors.New("workflow input store requires tenant, workflow, step, and input")
	}
	if len(text) > 1<<20 {
		return "", errors.New("workflow provider input exceeds 1 MiB")
	}
	ref := fmt.Sprintf("%s/%s/%s", tenantID, workflowID, stepID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.values[ref]; ok && existing != text {
		return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
	}
	s.values[ref] = text
	return ref, nil
}

func (s *MemoryInputStore) Get(_ context.Context, tenantID, ref string) (string, error) {
	if s == nil || tenantID == "" || ref == "" {
		return "", errors.New("workflow input lookup requires tenant and reference")
	}
	if len(ref) <= len(tenantID) || ref[:len(tenantID)] != tenantID || ref[len(tenantID)] != '/' {
		return "", core.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[ref]
	if !ok {
		return "", core.ErrNotFound
	}
	return value, nil
}
