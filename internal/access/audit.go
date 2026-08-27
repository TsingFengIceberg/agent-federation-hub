package access

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
