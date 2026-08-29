package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// FileOutboxPublisher is a small operator-visible sink for development and
// single-process deployments. Each idempotency key is written once and the
// file is fsynced after every append.
type FileOutboxPublisher struct {
	mu   sync.Mutex
	file *os.File
	seen map[string]struct{}
}

func NewFileOutboxPublisher(path string) (*FileOutboxPublisher, error) {
	if path == "" {
		return nil, errors.New("outbox file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(path)), 0o750); err != nil {
		return nil, fmt.Errorf("create outbox file directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open outbox file: %w", err)
	}
	publisher := &FileOutboxPublisher{file: file, seen: make(map[string]struct{})}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var envelope struct {
			TenantID string `json:"tenantId"`
			DedupKey string `json:"dedupKey"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) == nil && envelope.TenantID != "" && envelope.DedupKey != "" {
			publisher.seen[envelope.TenantID+":"+envelope.DedupKey] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("read outbox file: %w", err)
	}
	return publisher, nil
}

func (p *FileOutboxPublisher) Publish(_ context.Context, item core.OutboxItem) error {
	if p == nil {
		return errors.New("outbox file publisher is closed")
	}
	key := item.TenantID + ":" + item.DedupKey
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return errors.New("outbox file publisher is closed")
	}
	if _, exists := p.seen[key]; exists {
		return nil
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode file outbox item: %w", err)
	}
	payload = append(payload, '\n')
	if _, err := p.file.Write(payload); err != nil {
		return fmt.Errorf("append file outbox item: %w", err)
	}
	if err := p.file.Sync(); err != nil {
		return fmt.Errorf("sync file outbox item: %w", err)
	}
	p.seen[key] = struct{}{}
	return nil
}

func (p *FileOutboxPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}
