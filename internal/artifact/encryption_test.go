package artifact

import (
	"context"
	"io"
	"strings"
	"testing"
)

type memoryObjects struct{ values map[string][]byte }

func (m *memoryObjects) Put(_ context.Context, key string, source io.Reader, size int64, mediaType string) error {
	data, err := io.ReadAll(source)
	if err != nil || int64(len(data)) != size {
		return err
	}
	if m.values == nil {
		m.values = make(map[string][]byte)
	}
	m.values[key] = append([]byte(nil), data...)
	return nil
}
func (m *memoryObjects) Open(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	data, ok := m.values[key]
	if !ok {
		return nil, ObjectInfo{}, ErrUnavailable
	}
	return io.NopCloser(strings.NewReader(string(data))), ObjectInfo{SizeBytes: int64(len(data)), MediaType: "text/plain"}, nil
}
func (m *memoryObjects) Delete(_ context.Context, key string) error {
	delete(m.values, key)
	return nil
}

func TestEncryptedStoreRoundTripAndCiphertextIsolation(t *testing.T) {
	inner := &memoryObjects{}
	store := &EncryptedStore{Inner: inner, Keys: StaticKeyProvider{KeyID: "k-1", Key: []byte("01234567890123456789012345678901")}}
	ctx := WithTenantKeyContext(context.Background(), "tenant-a")
	plaintext := "opaque artifact payload"
	if err := store.Put(ctx, "aa/key", strings.NewReader(plaintext), int64(len(plaintext)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if string(inner.values["aa/key"]) == plaintext {
		t.Fatal("plaintext was stored")
	}
	reader, info, err := store.Open(ctx, "aa/key")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(decrypted) != plaintext || info.SizeBytes != int64(len(plaintext)) {
		t.Fatalf("payload=%q info=%+v", decrypted, info)
	}
	if _, _, err := store.Open(WithTenantKeyContext(context.Background(), "tenant-b"), "aa/key"); err == nil {
		t.Fatal("cross-tenant context decrypted object")
	}
}

func TestEncryptedStoreRejectsUnknownKeyAndSizeMismatch(t *testing.T) {
	inner := &memoryObjects{}
	store := &EncryptedStore{Inner: inner, Keys: StaticKeyProvider{KeyID: "k-1", Key: []byte("01234567890123456789012345678901")}}
	if err := store.Put(context.Background(), "aa/key", strings.NewReader("x"), 2, "text/plain"); err == nil {
		t.Fatal("size mismatch accepted")
	}
	store.Keys = StaticKeyProvider{KeyID: "k-2", Key: []byte("01234567890123456789012345678901")}
	if err := store.Put(context.Background(), "aa/key", strings.NewReader("x"), 1, "text/plain"); err != nil {
		t.Fatal(err)
	}
	store.Keys = StaticKeyProvider{KeyID: "k-1", Key: []byte("01234567890123456789012345678901")}
	if _, _, err := store.Open(context.Background(), "aa/key"); err == nil {
		t.Fatal("unknown key was accepted")
	}
}

func TestKeyRingReadsPreviousKeyAfterRotation(t *testing.T) {
	inner := &memoryObjects{}
	keys := map[string][]byte{
		"k-1": []byte("01234567890123456789012345678901"),
		"k-2": []byte("abcdefabcdefabcdefabcdefabcdefab"),
	}
	store := &EncryptedStore{Inner: inner, Keys: KeyRingProvider{CurrentID: "k-1", Keys: keys}}
	if err := store.Put(context.Background(), "aa/key", strings.NewReader("old"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	store.Keys = KeyRingProvider{CurrentID: "k-2", Keys: keys}
	if err := store.Put(context.Background(), "aa/key2", strings.NewReader("new"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"aa/key": "old", "aa/key2": "new"} {
		reader, _, err := store.Open(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(reader)
		_ = reader.Close()
		if string(got) != want {
			t.Fatalf("key=%s got=%q", key, got)
		}
	}
	delete(keys, "k-1")
	if _, _, err := store.Open(context.Background(), "aa/key"); err == nil {
		t.Fatal("retired key still decrypted object")
	}
}
