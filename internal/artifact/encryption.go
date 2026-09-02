package artifact

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
)

// KeyProvider is the Hub/KMS boundary for Artifact encryption. Implementations
// must keep key material outside configuration, journals, and object metadata.
// The tenant is supplied through context so a provider can select a tenant key
// without coupling the Artifact package to a particular KMS API.
type KeyProvider interface {
	Current(ctx context.Context) (keyID string, key []byte, err error)
	ByID(ctx context.Context, keyID string) ([]byte, error)
}

type tenantContextKey struct{}

// WithTenantKeyContext scopes a key lookup to a tenant. Services should use
// this helper before calling an encrypted ObjectStore.
func WithTenantKeyContext(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenantID)
}

func tenantFromContext(ctx context.Context) string {
	value, _ := ctx.Value(tenantContextKey{}).(string)
	return value
}

// StaticKeyProvider is suitable for local development and deterministic tests.
// Production deployments should replace it with a KMS-backed implementation.
type StaticKeyProvider struct {
	KeyID string
	Key   []byte
}

// KeyRingProvider supports online key rotation: new objects use CurrentID,
// while existing envelopes remain readable until their old key is retired.
type KeyRingProvider struct {
	CurrentID string
	Keys      map[string][]byte
}

func (p KeyRingProvider) Current(_ context.Context) (string, []byte, error) {
	key, ok := p.Keys[p.CurrentID]
	if !ok || len(key) == 0 || p.CurrentID == "" {
		return "", nil, errors.New("current encryption key is not configured")
	}
	return p.CurrentID, append([]byte(nil), key...), nil
}

func (p KeyRingProvider) ByID(_ context.Context, keyID string) ([]byte, error) {
	key, ok := p.Keys[keyID]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("encryption key %q is unavailable", keyID)
	}
	return append([]byte(nil), key...), nil
}

func (p StaticKeyProvider) Current(_ context.Context) (string, []byte, error) {
	if p.KeyID == "" || len(p.Key) == 0 {
		return "", nil, errors.New("static encryption key is not configured")
	}
	return p.KeyID, append([]byte(nil), p.Key...), nil
}

func (p StaticKeyProvider) ByID(_ context.Context, keyID string) ([]byte, error) {
	if keyID != p.KeyID || len(p.Key) == 0 {
		return nil, fmt.Errorf("encryption key %q is unavailable", keyID)
	}
	return append([]byte(nil), p.Key...), nil
}

// EncryptedStore encrypts object bytes before delegating to another store. The
// envelope contains only a version, key ID, nonce, and authenticated ciphertext;
// tenant identity and plaintext are never stored in the envelope.
type EncryptedStore struct {
	Inner ObjectStore
	Keys  KeyProvider
}

var envelopeMagic = [4]byte{'A', 'F', 'H', '1'}

func (s *EncryptedStore) Put(ctx context.Context, key string, source io.Reader, size int64, mediaType string) error {
	if s == nil || s.Inner == nil || s.Keys == nil {
		return errors.New("encrypted object store is not configured")
	}
	if size < 0 || size > 128<<20 {
		return errors.New("encrypted object size is outside the supported range")
	}
	plaintext, err := io.ReadAll(io.LimitReader(source, size+1))
	if err != nil {
		return err
	}
	if int64(len(plaintext)) != size {
		return fmt.Errorf("object size mismatch: read %d bytes, expected %d", len(plaintext), size)
	}
	keyID, rawKey, err := s.Keys.Current(ctx)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return fmt.Errorf("create artifact cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create artifact AEAD: %w", err)
	}
	if len(keyID) > 65535 {
		return errors.New("artifact encryption key ID is too long")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	associated := []byte(key + "\x00" + tenantFromContext(ctx))
	ciphertext := aead.Seal(nil, nonce, plaintext, associated)
	envelope := make([]byte, 8+len(keyID)+len(nonce)+len(ciphertext))
	copy(envelope[:4], envelopeMagic[:])
	binary.BigEndian.PutUint16(envelope[4:6], uint16(len(keyID)))
	envelope[6] = byte(len(nonce))
	// Byte 7 is reserved for future envelope flags.
	copy(envelope[8:8+len(keyID)], keyID)
	position := 8 + len(keyID)
	copy(envelope[position:position+len(nonce)], nonce)
	position += len(nonce)
	copy(envelope[position:], ciphertext)
	return s.Inner.Put(ctx, key, bytes.NewReader(envelope), int64(len(envelope)), mediaType)
}

func (s *EncryptedStore) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if s == nil || s.Inner == nil || s.Keys == nil {
		return nil, ObjectInfo{}, errors.New("encrypted object store is not configured")
	}
	reader, info, err := s.Inner.Open(ctx, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	defer reader.Close()
	envelope, err := io.ReadAll(io.LimitReader(reader, 128<<20+1))
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	if int64(len(envelope)) != info.SizeBytes || len(envelope) < 8 || !bytes.Equal(envelope[:4], envelopeMagic[:]) {
		return nil, ObjectInfo{}, errors.New("artifact encryption envelope is invalid")
	}
	keyLength := int(binary.BigEndian.Uint16(envelope[4:6]))
	nonceLength := int(envelope[6])
	if keyLength == 0 || nonceLength == 0 || 8+keyLength+nonceLength >= len(envelope) {
		return nil, ObjectInfo{}, errors.New("artifact encryption envelope is truncated")
	}
	keyID := string(envelope[8 : 8+keyLength])
	nonceStart := 8 + keyLength
	nonce := envelope[nonceStart : nonceStart+nonceLength]
	ciphertext := envelope[nonceStart+nonceLength:]
	rawKey, err := s.Keys.ByID(ctx, keyID)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("create artifact cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("create artifact AEAD: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ObjectInfo{}, errors.New("artifact encryption nonce has an invalid size")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(key+"\x00"+tenantFromContext(ctx)))
	if err != nil {
		return nil, ObjectInfo{}, errors.New("artifact encryption authentication failed")
	}
	return io.NopCloser(bytes.NewReader(plaintext)), ObjectInfo{SizeBytes: int64(len(plaintext)), MediaType: info.MediaType}, nil
}

func (s *EncryptedStore) Delete(ctx context.Context, key string) error {
	if s == nil || s.Inner == nil {
		return errors.New("encrypted object store is not configured")
	}
	return s.Inner.Delete(ctx, key)
}

func (s *EncryptedStore) Health(ctx context.Context) error {
	if s == nil || s.Inner == nil || s.Keys == nil {
		return errors.New("encrypted object store is not configured")
	}
	if health, ok := s.Inner.(HealthStore); ok {
		if err := health.Health(ctx); err != nil {
			return err
		}
	}
	_, _, err := s.Keys.Current(ctx)
	return err
}
