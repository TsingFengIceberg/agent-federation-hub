package access

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	mu       sync.Mutex
	file     *os.File
	sequence uint64
	previous string
}

func OpenFileAuditSink(path string) (*FileAuditSink, error) {
	if path == "" {
		return nil, fmt.Errorf("audit file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure audit file: %w", err)
	}
	sink := &FileAuditSink{file: file}
	if err := sink.loadChainState(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return sink, nil
}

func (s *FileAuditSink) Record(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return fmt.Errorf("audit file is closed")
	}
	record.Version = 1
	record.Sequence = s.sequence + 1
	record.PreviousHash = s.previous
	record.IntegrityHash = ""
	digest, err := auditDigest(record)
	if err != nil {
		return err
	}
	record.IntegrityHash = digest
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if _, err := s.file.Write(encoded); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	s.sequence = record.Sequence
	s.previous = record.IntegrityHash
	return nil
}

func (s *FileAuditSink) loadChainState() error {
	if _, err := s.file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek audit file: %w", err)
	}
	scanner := bufio.NewScanner(s.file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var previous string
	var sequence uint64
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record AuditRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("decode audit record: %w", err)
		}
		if record.IntegrityHash != "" {
			if record.PreviousHash != previous || record.Sequence != sequence+1 {
				return errors.New("audit integrity chain is broken")
			}
			want, err := auditDigest(record)
			if err != nil || want != record.IntegrityHash {
				return errors.New("audit integrity hash is invalid")
			}
			previous = record.IntegrityHash
			sequence = record.Sequence
		} else {
			// Legacy records predate the chain. Preserve their bytes as the
			// anchor while making the next record verifiable.
			hash := sha256.Sum256(line)
			previous = hex.EncodeToString(hash[:])
			sequence++
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read audit file: %w", err)
	}
	if _, err := s.file.Seek(0, 2); err != nil {
		return fmt.Errorf("seek audit append position: %w", err)
	}
	s.sequence, s.previous = sequence, previous
	return nil
}

func auditDigest(record AuditRecord) (string, error) {
	record.IntegrityHash = ""
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
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

// RetryingAuditSink retries a downstream exporter for short-lived failures.
// It is intentionally bounded and does not replace a durable outbox; callers
// should pair it with FileAuditSink when audit loss must be recoverable.
type RetryingAuditSink struct {
	Sink           AuditSink
	Attempts       int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Sleep          func(context.Context, time.Duration) error
}

func (s *RetryingAuditSink) Record(ctx context.Context, record AuditRecord) error {
	if s == nil || s.Sink == nil {
		return errors.New("retrying audit sink is not configured")
	}
	attempts := s.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	initial := s.InitialBackoff
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	max := s.MaxBackoff
	if max <= 0 {
		max = 2 * time.Second
	}
	sleep := s.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	var lastErr error
	backoff := initial
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := s.Sink.Record(ctx, record); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts {
			break
		}
		if err := sleep(ctx, backoff); err != nil {
			return fmt.Errorf("audit export retry interrupted: %w", err)
		}
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}
	return fmt.Errorf("audit export failed after %d attempts: %w", attempts, lastErr)
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
