package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
)

func TestFileInputStorePersistsEncryptedTenantBoundInput(t *testing.T) {
	root := t.TempDir()
	keys := artifactstore.KeyRingProvider{CurrentID: "k2", Keys: map[string][]byte{
		"k1": []byte("01234567890123456789012345678901"),
		"k2": []byte("abcdefghijklmnopqrstuvwxyzABCDEF"),
	}}
	store, err := NewFileInputStore(filepath.Join(root, "vault"), keys)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), "tenant-a", "wf-1", "step-1", "private prompt")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "tenant-a/wf-1/step-1" {
		t.Fatalf("unexpected ref %q", ref)
	}
	files, err := os.ReadDir(filepath.Join(root, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d vault files, want 1", len(files))
	}
	contents, err := os.ReadFile(filepath.Join(root, "vault", files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "private prompt") {
		t.Fatal("plaintext input leaked into envelope")
	}
	info, err := files[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("vault file mode %o, want 600", info.Mode().Perm())
	}

	restarted, err := NewFileInputStore(filepath.Join(root, "vault"), keys)
	if err != nil {
		t.Fatal(err)
	}
	value, err := restarted.Get(context.Background(), "tenant-a", ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != "private prompt" {
		t.Fatalf("got %q", value)
	}
	_, err = restarted.Get(context.Background(), "tenant-b", ref)
	if err == nil {
		t.Fatal("cross-tenant lookup unexpectedly succeeded")
	}
}

func TestFileInputStoreDetectsTamperingAndSupportsKeyRotation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	oldKeys := artifactstore.KeyRingProvider{CurrentID: "old", Keys: map[string][]byte{"old": []byte("01234567890123456789012345678901")}}
	store, err := NewFileInputStore(root, oldKeys)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), "tenant", "workflow", "step", "value")
	if err != nil {
		t.Fatal(err)
	}
	path := store.pathFor(ref)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(context.Background(), "tenant", ref)
	if err == nil {
		t.Fatal("tampered input unexpectedly decrypted")
	}

	// Write a second valid envelope and verify a rotated provider can still read
	// an envelope created with the retired key. A tampered reference is never
	// silently overwritten.
	ref, err = store.Put(context.Background(), "tenant", "workflow", "step-2", "value")
	if err != nil {
		t.Fatal(err)
	}
	rotated := artifactstore.KeyRingProvider{CurrentID: "new", Keys: map[string][]byte{
		"old": []byte("01234567890123456789012345678901"),
		"new": []byte("abcdefghijklmnopqrstuvwxyzABCDEF"),
	}}
	rotatedStore, err := NewFileInputStore(root, rotated)
	if err != nil {
		t.Fatal(err)
	}
	value, err := rotatedStore.Get(context.Background(), "tenant", ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != "value" {
		t.Fatalf("got %q", value)
	}
	newRef, err := rotatedStore.Put(context.Background(), "tenant", "workflow", "step-3", "new value")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGetInput(t, rotatedStore, "tenant", newRef); got != "new value" {
		t.Fatalf("got %q", got)
	}
}

func TestFileInputStoreConcurrentConflictingPutDoesNotOverwrite(t *testing.T) {
	keys := artifactstore.KeyRingProvider{CurrentID: "key", Keys: map[string][]byte{
		"key": []byte("01234567890123456789012345678901"),
	}}
	store, err := NewFileInputStore(filepath.Join(t.TempDir(), "vault"), keys)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for _, value := range []string{"first", "second"} {
		wait.Add(1)
		go func(value string) {
			defer wait.Done()
			_, putErr := store.Put(context.Background(), "tenant", "workflow", "step", value)
			errs <- putErr
		}(value)
	}
	wait.Wait()
	close(errs)
	var successes, conflicts int
	for putErr := range errs {
		if putErr == nil {
			successes++
		} else if strings.Contains(putErr.Error(), "already contains different input") {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent Put error: %v", putErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one each", successes, conflicts)
	}
	value, err := store.Get(context.Background(), "tenant", "tenant/workflow/step")
	if err != nil {
		t.Fatal(err)
	}
	if value != "first" && value != "second" {
		t.Fatalf("unexpected installed value %q", value)
	}
}

func mustGetInput(t *testing.T, store *FileInputStore, tenant, ref string) string {
	t.Helper()
	value, err := store.Get(context.Background(), tenant, ref)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
