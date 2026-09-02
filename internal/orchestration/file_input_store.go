package orchestration

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// FileInputStore is the restart-safe Workflow input vault. Only an opaque
// reference is kept in the Workflow aggregate; the file contains an
// authenticated envelope bound to both tenant and reference. The key provider
// is intentionally the same replaceable boundary used by Artifact storage so
// deployments can move from local key rings to KMS without changing workflow
// code.
type FileInputStore struct {
	Root     string
	Keys     artifactstore.KeyProvider
	MaxBytes int
	FileMode os.FileMode
}

var inputEnvelopeMagic = [4]byte{'A', 'F', 'I', '1'}

func NewFileInputStore(root string, keys artifactstore.KeyProvider) (*FileInputStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workflow input vault root is required")
	}
	if keys == nil {
		return nil, errors.New("workflow input vault key provider is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create workflow input vault: %w", err)
	}
	if err := os.Chmod(root, 0o750); err != nil {
		return nil, fmt.Errorf("secure workflow input vault: %w", err)
	}
	return &FileInputStore{Root: filepath.Clean(root), Keys: keys, MaxBytes: 1 << 20, FileMode: 0o600}, nil
}

func (s *FileInputStore) Put(ctx context.Context, tenantID, workflowID, stepID, text string) (string, error) {
	if s == nil || s.Keys == nil || s.Root == "" || tenantID == "" || workflowID == "" || stepID == "" || text == "" {
		return "", errors.New("workflow input vault requires tenant, workflow, step, and input")
	}
	if len(text) > s.maxBytes() {
		return "", errors.New("workflow provider input exceeds configured limit")
	}
	ref := fmt.Sprintf("%s/%s/%s", tenantID, workflowID, stepID)
	path := s.pathFor(ref)
	if existing, err := s.Get(ctx, tenantID, ref); err == nil {
		if existing != text {
			return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
		}
		return ref, nil
	} else if !errors.Is(err, core.ErrNotFound) {
		return "", err
	}
	keyID, rawKey, err := s.Keys.Current(artifactstore.WithTenantKeyContext(ctx, tenantID))
	if err != nil {
		return "", err
	}
	aead, err := newInputAEAD(rawKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	if len(keyID) > 65535 {
		return "", errors.New("workflow input key ID is too long")
	}
	ciphertext := aead.Seal(nil, nonce, []byte(text), []byte(tenantID+"\x00"+ref))
	envelope := make([]byte, 8+len(keyID)+len(nonce)+len(ciphertext))
	copy(envelope[:4], inputEnvelopeMagic[:])
	binary.BigEndian.PutUint16(envelope[4:6], uint16(len(keyID)))
	envelope[6] = byte(len(nonce))
	copy(envelope[8:8+len(keyID)], keyID)
	position := 8 + len(keyID)
	copy(envelope[position:position+len(nonce)], nonce)
	copy(envelope[position+len(nonce):], ciphertext)
	if err := s.atomicWrite(path, envelope); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, getErr := s.Get(ctx, tenantID, ref)
			if getErr != nil {
				return "", getErr
			}
			if existing != text {
				return "", fmt.Errorf("workflow input reference %q already contains different input", ref)
			}
			return ref, nil
		}
		return "", err
	}
	return ref, nil
}

func (s *FileInputStore) Get(ctx context.Context, tenantID, ref string) (string, error) {
	if s == nil || s.Keys == nil || s.Root == "" || tenantID == "" || ref == "" {
		return "", errors.New("workflow input lookup requires tenant and reference")
	}
	if !strings.HasPrefix(ref, tenantID+"/") || strings.Contains(ref, "..") || strings.ContainsAny(ref, "\\\n\r") {
		return "", core.ErrNotFound
	}
	envelope, err := os.ReadFile(s.pathFor(ref))
	if errors.Is(err, os.ErrNotExist) {
		return "", core.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read workflow input: %w", err)
	}
	if len(envelope) < 8 || !bytes.Equal(envelope[:4], inputEnvelopeMagic[:]) {
		return "", errors.New("workflow input envelope is invalid")
	}
	keyLength := int(binary.BigEndian.Uint16(envelope[4:6]))
	nonceLength := int(envelope[6])
	if keyLength == 0 || nonceLength == 0 || 8+keyLength+nonceLength >= len(envelope) {
		return "", errors.New("workflow input envelope is truncated")
	}
	keyID := string(envelope[8 : 8+keyLength])
	nonceStart := 8 + keyLength
	nonce := envelope[nonceStart : nonceStart+nonceLength]
	rawKey, err := s.Keys.ByID(artifactstore.WithTenantKeyContext(ctx, tenantID), keyID)
	if err != nil {
		return "", err
	}
	aead, err := newInputAEAD(rawKey)
	if err != nil {
		return "", err
	}
	if len(nonce) != aead.NonceSize() {
		return "", errors.New("workflow input nonce has an invalid size")
	}
	plaintext, err := aead.Open(nil, nonce, envelope[nonceStart+nonceLength:], []byte(tenantID+"\x00"+ref))
	if err != nil {
		return "", errors.New("workflow input authentication failed")
	}
	if len(plaintext) == 0 || len(plaintext) > s.maxBytes() {
		return "", errors.New("workflow input exceeds configured limit")
	}
	return string(plaintext), nil
}

func (s *FileInputStore) maxBytes() int {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return 1 << 20
}

func (s *FileInputStore) pathFor(ref string) string {
	return filepath.Join(s.Root, core.DigestString(ref)+".input")
}

func (s *FileInputStore) atomicWrite(path string, data []byte) error {
	temporary, err := os.CreateTemp(s.Root, ".input-*")
	if err != nil {
		return fmt.Errorf("create workflow input temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	mode := s.FileMode
	if mode == 0 {
		mode = 0o600
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write workflow input: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync workflow input: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Link creates the destination atomically and never replaces an existing
	// reference. This preserves conflict detection even when two Hub
	// processes write the same tenant/workflow/step concurrently.
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("install workflow input: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove workflow input temporary file: %w", err)
	}
	directory, err := os.Open(s.Root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func newInputAEAD(rawKey []byte) (cipher.AEAD, error) {
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
