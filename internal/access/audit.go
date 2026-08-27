package access

import (
	"context"
	"encoding/json"
	"io"
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
