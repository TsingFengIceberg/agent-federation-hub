package artifact

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type scannerFunc func(context.Context, io.Reader) (core.ArtifactScanStatus, error)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f scannerFunc) Scan(ctx context.Context, source io.Reader) (core.ArtifactScanStatus, error) {
	return f(ctx, source)
}

func newTestService(t *testing.T, scanner Scanner, now time.Time) (*Service, *core.JournalStore) {
	t.Helper()
	metadata, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Service{
		Metadata: metadata, Objects: objects, Scanner: scanner, Now: func() time.Time { return now },
		Policy: Policy{
			MaxBytes: 1024, AllowedMIME: map[string]struct{}{"text/plain": {}, "application/json": {}},
			Quota: Quota{MaxBytes: 2048, MaxObjects: 2}, Retention: time.Hour,
		},
	}, metadata
}

func TestIngestIsIdempotentAndContentAddressed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	service, metadata := newTestService(t, scannerFunc(func(_ context.Context, source io.Reader) (core.ArtifactScanStatus, error) {
		payload, _ := io.ReadAll(source)
		if string(payload) != "hello federation" {
			t.Fatalf("scanner payload=%q", payload)
		}
		return core.ArtifactScanClean, nil
	}), now)
	input := Input{
		TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "result-a",
		DedupKey: "observation-a", PartIndex: 0, MediaType: "text/plain; charset=utf-8", Filename: "result.txt",
	}
	first, err := service.Ingest(context.Background(), input, strings.NewReader("hello federation"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Ingest(context.Background(), input, strings.NewReader("hello federation"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Status != core.ArtifactObjectAvailable || second.ScanStatus != core.ArtifactScanClean {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	usage, err := metadata.GetArtifactUsage(context.Background(), input.TenantID)
	if err != nil || usage.Bytes != int64(len("hello federation")) || usage.Objects != 1 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	reader, object, err := service.Open(context.Background(), input.TenantID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	payload, _ := io.ReadAll(reader)
	if string(payload) != "hello federation" || object.StorageKey == "" {
		t.Fatalf("payload=%q object=%+v", payload, object)
	}
}

func TestPolicyScanFailureAndQuarantineAreFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	service, metadata := newTestService(t, scannerFunc(func(context.Context, io.Reader) (core.ArtifactScanStatus, error) {
		return core.ArtifactScanError, errors.New("scanner unavailable")
	}), now)
	input := Input{TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "result-a", DedupKey: "scan-error", PartIndex: 0, MediaType: "text/plain"}
	if _, err := service.Ingest(context.Background(), input, strings.NewReader("payload")); err == nil {
		t.Fatal("scanner failure was accepted")
	}
	usage, _ := metadata.GetArtifactUsage(context.Background(), input.TenantID)
	if usage.Bytes != 0 || usage.Objects != 0 {
		t.Fatalf("failed scan retained quota: %+v", usage)
	}

	service.Scanner = scannerFunc(func(context.Context, io.Reader) (core.ArtifactScanStatus, error) {
		return core.ArtifactScanInfected, nil
	})
	input.DedupKey = "infected"
	quarantined, err := service.Ingest(context.Background(), input, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if quarantined.Status != core.ArtifactObjectQuarantined {
		t.Fatalf("quarantined=%+v", quarantined)
	}
	if _, _, err := service.Open(context.Background(), input.TenantID, quarantined.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("quarantined content open error=%v", err)
	}

	if _, err := service.Ingest(context.Background(), Input{
		TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "result-a",
		DedupKey: "mime", PartIndex: 1, MediaType: "application/pdf",
	}, strings.NewReader("plain text")); !errors.Is(err, ErrPolicy) {
		t.Fatalf("MIME mismatch error=%v", err)
	}
}

func TestURIImportSanitizesQueryAndLifecycleReleasesQuota(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	service, metadata := newTestService(t, scannerFunc(func(context.Context, io.Reader) (core.ArtifactScanStatus, error) {
		return core.ArtifactScanClean, nil
	}), now)
	service.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" {
			t.Fatal("URI importer forwarded authorization")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	object, err := service.IngestURI(context.Background(), Input{
		TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "result-a",
		DedupKey: "uri", PartIndex: 0,
	}, "https://artifact.example/result?signature=secret#fragment")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(object.SourceURI, "secret") || strings.Contains(object.SourceURI, "fragment") {
		t.Fatalf("source URI retained sensitive query or fragment: %s", object.SourceURI)
	}
	service.Now = func() time.Time { return now.Add(2 * time.Hour) }
	lifecycle := &Lifecycle{
		Metadata: metadata, Objects: service.Objects, WorkerID: "worker-a",
		Now: service.Now, LeaseDuration: time.Minute,
	}
	if err := lifecycle.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleted, err := metadata.GetArtifact(context.Background(), object.TenantID, object.ID)
	if err != nil || deleted.Status != core.ArtifactObjectDeleted {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	usage, _ := metadata.GetArtifactUsage(context.Background(), object.TenantID)
	if usage.Bytes != 0 || usage.Objects != 0 {
		t.Fatalf("deleted object retained quota: %+v", usage)
	}
	if _, _, err := service.Open(context.Background(), object.TenantID, object.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted content open error=%v", err)
	}
}

func TestFileStoreRejectsArbitraryKeysAndExactSize(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "../escape", bytes.NewReader(nil), 0, ""); err == nil {
		t.Fatal("arbitrary object key was accepted")
	}
	key := strings.Repeat("a", 2) + "/" + strings.Repeat("b", 64)
	if err := store.Put(context.Background(), key, strings.NewReader("too long"), 3, "text/plain"); err == nil {
		t.Fatal("size mismatch was accepted")
	}
}
