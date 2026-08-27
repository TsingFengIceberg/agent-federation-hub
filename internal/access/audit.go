package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type JSONAuditSink struct {
	mu     sync.Mutex
	writer io.Writer
}

func NewJSONAuditSink(writer io.Writer) *JSONAuditSink {
	return &JSONAuditSink{writer: writer}
}

func (s *JSONAuditSink) Record(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.NewEncoder(s.writer).Encode(record)
}

type FileAuditSink struct {
	mu   sync.Mutex
	file *os.File
}

func OpenFileAuditSink(path string) (*FileAuditSink, error) {
	if path == "" {
		return nil, fmt.Errorf("audit file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure audit file: %w", err)
	}
	return &FileAuditSink{file: file}, nil
}

func (s *FileAuditSink) Record(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return fmt.Errorf("audit file is closed")
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if _, err := s.file.Write(encoded); err != nil {
		return err
	}
	return s.file.Sync()
}

func (s *FileAuditSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// AuditSinkFunc adapts a function to AuditSink, useful for central exporters
// and tests without coupling access control to one telemetry vendor.
type AuditSinkFunc func(context.Context, AuditRecord) error

func (fn AuditSinkFunc) Record(ctx context.Context, record AuditRecord) error {
	return fn(ctx, record)
}

// FanoutAuditSink writes to every configured sink and returns a combined error.
// A local durable sink can therefore be paired with a central exporter: a
// central outage never loses the local record, while the error remains visible
// to callers that choose to enforce export health.
type FanoutAuditSink []AuditSink

func (s FanoutAuditSink) Record(ctx context.Context, record AuditRecord) error {
	var combined error
	for _, sink := range s {
		if sink == nil {
			continue
		}
		if err := sink.Record(ctx, record); err != nil {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}

// HTTPAuditSink exports redacted audit records to an HTTPS collector. The
// caller supplies a short-lived token through a callback so credentials never
// enter configuration structs or records.
type HTTPAuditSink struct {
	Endpoint         string
	Client           *http.Client
	Bearer           func(context.Context) (string, error)
	MaxResponseBytes int64
}

func NewHTTPAuditSink(endpoint string, bearer func(context.Context) (string, error)) (*HTTPAuditSink, error) {
	if !strings.HasPrefix(endpoint, "https://") {
		return nil, fmt.Errorf("audit endpoint must use HTTPS")
	}
	return &HTTPAuditSink{Endpoint: endpoint, Bearer: bearer}, nil
}

func (s *HTTPAuditSink) Record(ctx context.Context, record AuditRecord) error {
	if s == nil || s.Endpoint == "" {
		return errors.New("audit endpoint is not configured")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode audit record: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create audit request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if s.Bearer != nil {
		token, err := s.Bearer(ctx)
		if err != nil {
			return fmt.Errorf("resolve audit credential: %w", err)
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("audit credential is empty")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send audit record: %w", err)
	}
	defer response.Body.Close()
	limit := s.MaxResponseBytes
	if limit <= 0 {
		limit = 64 << 10
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if readErr != nil || int64(len(body)) > limit {
		return errors.New("audit collector response exceeds configured limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("audit collector returned HTTP %d", response.StatusCode)
	}
	return nil
}
