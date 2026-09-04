package orchestration

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/jackc/pgx/v5"
)

// PostgresInputStore is the multi-instance durable Workflow input vault. Only
// an authenticated encrypted envelope is stored in PostgreSQL; the Hub
// Workflow aggregate still contains only its opaque InputRef and digest.
type PostgresInputStore struct {
	DB       core.SQLExecutor
	Keys     artifactstore.KeyProvider
	MaxBytes int
}

func NewPostgresInputStore(db core.SQLExecutor, keys artifactstore.KeyProvider) (*PostgresInputStore, error) {
	if db == nil {
		return nil, errors.New("workflow input PostgreSQL executor is required")
	}
	if keys == nil {
		return nil, errors.New("workflow input vault key provider is required")
	}
	return &PostgresInputStore{DB: db, Keys: keys, MaxBytes: 1 << 20}, nil
}

func (s *PostgresInputStore) Put(ctx context.Context, tenantID, workflowID, stepID string, input WorkflowInput) (string, error) {
	if s == nil || s.DB == nil || s.Keys == nil || tenantID == "" || workflowID == "" || stepID == "" {
		return "", errors.New("workflow input PostgreSQL store requires tenant, workflow, step, and input")
	}
	if err := input.validate(); err != nil {
		return "", err
	}
	ref := fmt.Sprintf("%s/%s/%s", tenantID, workflowID, stepID)
	if existing, err := s.Get(ctx, tenantID, ref); err == nil {
		if !workflowInputsEqual(existing, input) {
			return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
		}
		return ref, nil
	} else if !errors.Is(err, core.ErrNotFound) {
		return "", err
	}
	envelope, err := sealPostgresInput(ctx, s.Keys, tenantID, ref, input, s.maxBytes())
	if err != nil {
		return "", err
	}
	if _, err := s.DB.Exec(ctx, `
		INSERT INTO afh_workflow_inputs (tenant_id, reference, payload, size_bytes)
		VALUES ($1, $2, $3, $4) ON CONFLICT (tenant_id, reference) DO NOTHING`, tenantID, ref, envelope, len(envelope)); err != nil {
		return "", fmt.Errorf("insert workflow input: %w", err)
	}
	existing, err := s.Get(ctx, tenantID, ref)
	if err != nil {
		return "", err
	}
	if !workflowInputsEqual(existing, input) {
		return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
	}
	return ref, nil
}

func (s *PostgresInputStore) Get(ctx context.Context, tenantID, ref string) (WorkflowInput, error) {
	if s == nil || s.DB == nil || s.Keys == nil || tenantID == "" || ref == "" {
		return WorkflowInput{}, errors.New("workflow input PostgreSQL lookup requires tenant and reference")
	}
	if !strings.HasPrefix(ref, tenantID+"/") || strings.Contains(ref, "..") || strings.ContainsAny(ref, "\\\n\r") {
		return WorkflowInput{}, core.ErrNotFound
	}
	var envelope []byte
	if err := s.DB.QueryRow(ctx, `SELECT payload FROM afh_workflow_inputs WHERE tenant_id=$1 AND reference=$2`, tenantID, ref).Scan(&envelope); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkflowInput{}, core.ErrNotFound
		}
		return WorkflowInput{}, fmt.Errorf("read workflow input: %w", err)
	}
	return openPostgresInput(ctx, s.Keys, tenantID, ref, envelope, s.maxBytes())
}

func (s *PostgresInputStore) maxBytes() int {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return 1 << 20
}

func sealPostgresInput(ctx context.Context, keys artifactstore.KeyProvider, tenantID, ref string, input WorkflowInput, maxBytes int) ([]byte, error) {
	plaintext, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode workflow input: %w", err)
	}
	if len(plaintext) > maxBytes {
		return nil, errors.New("workflow provider input exceeds configured limit")
	}
	keyID, rawKey, err := keys.Current(artifactstore.WithTenantKeyContext(ctx, tenantID))
	if err != nil {
		return nil, err
	}
	aead, err := inputAEAD(rawKey)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	if len(keyID) > 65535 {
		return nil, errors.New("workflow input key ID is too long")
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(tenantID+"\x00"+ref))
	envelope := make([]byte, 8+len(keyID)+len(nonce)+len(ciphertext))
	copy(envelope[:4], inputEnvelopeMagic[:])
	binary.BigEndian.PutUint16(envelope[4:6], uint16(len(keyID)))
	envelope[6] = byte(len(nonce))
	copy(envelope[8:8+len(keyID)], keyID)
	position := 8 + len(keyID)
	copy(envelope[position:position+len(nonce)], nonce)
	copy(envelope[position+len(nonce):], ciphertext)
	return envelope, nil
}

func openPostgresInput(ctx context.Context, keys artifactstore.KeyProvider, tenantID, ref string, envelope []byte, maxBytes int) (WorkflowInput, error) {
	if len(envelope) < 8 || !bytes.Equal(envelope[:4], inputEnvelopeMagic[:]) {
		return WorkflowInput{}, errors.New("workflow input envelope is invalid")
	}
	keyLength := int(binary.BigEndian.Uint16(envelope[4:6]))
	nonceLength := int(envelope[6])
	if keyLength == 0 || nonceLength == 0 || 8+keyLength+nonceLength >= len(envelope) {
		return WorkflowInput{}, errors.New("workflow input envelope is truncated")
	}
	keyID := string(envelope[8 : 8+keyLength])
	nonceStart := 8 + keyLength
	nonce := envelope[nonceStart : nonceStart+nonceLength]
	rawKey, err := keys.ByID(artifactstore.WithTenantKeyContext(ctx, tenantID), keyID)
	if err != nil {
		return WorkflowInput{}, err
	}
	aead, err := inputAEAD(rawKey)
	if err != nil {
		return WorkflowInput{}, err
	}
	if len(nonce) != aead.NonceSize() {
		return WorkflowInput{}, errors.New("workflow input nonce has an invalid size")
	}
	plaintext, err := aead.Open(nil, nonce, envelope[nonceStart+nonceLength:], []byte(tenantID+"\x00"+ref))
	if err != nil {
		return WorkflowInput{}, errors.New("workflow input authentication failed")
	}
	if len(plaintext) == 0 || len(plaintext) > maxBytes {
		return WorkflowInput{}, errors.New("workflow input exceeds configured limit")
	}
	var input WorkflowInput
	if err := json.Unmarshal(plaintext, &input); err != nil {
		input = WorkflowInput{Text: string(plaintext)}
	}
	if err := input.validate(); err != nil {
		return WorkflowInput{}, err
	}
	return cloneWorkflowInput(input), nil
}

func inputAEAD(rawKey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return nil, fmt.Errorf("create workflow input cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create workflow input AEAD: %w", err)
	}
	return aead, nil
}
