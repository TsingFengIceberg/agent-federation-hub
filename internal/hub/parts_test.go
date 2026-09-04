package hub

import (
	"context"
	"encoding/base64"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

func TestSubmitTaskCarriesTextDataAndTenantBoundArtifactParts(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := artifactstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &artifactstore.Service{
		Metadata: store, Objects: objects,
		Policy: artifactstore.Policy{MaxBytes: 1024, AllowedMIME: map[string]struct{}{"text/plain": {}}, Quota: artifactstore.Quota{MaxBytes: 2048, MaxObjects: 4}, Retention: time.Hour},
	}
	object, err := artifacts.Ingest(context.Background(), artifactstore.Input{
		TenantID: "tenant-a", TaskID: "source-task", ArtifactID: "source-artifact", DedupKey: "part-input", PartIndex: 0,
		MediaType: "text/plain", Filename: "source.txt",
	}, strings.NewReader("shared artifact"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{descriptor: federation.Descriptor{Name: "parts", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"}, send: func(_ context.Context, _ core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
		return sequence(federation.Observation{RemoteTaskID: "remote-parts", RemoteContextID: "context-parts", State: core.TaskStateCompleted, Final: true})
	}}
	service := newTestService(t, store, adapter)
	service.Artifacts = artifacts
	registerTestAgent(t, service, "tenant-a")
	registerTestAgent(t, service, "tenant-b")
	task, err := service.SubmitTask(context.Background(), "tenant-a", SubmitTaskInput{AgentID: "agent-1", Text: "review this", Parts: []core.Part{
		{Kind: core.PartData, MediaType: "application/json", Data: map[string]any{"priority": "high"}},
		{Kind: core.PartFile, ObjectID: object.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != core.TaskStateCompleted || len(adapter.lastMessage.Parts) != 3 {
		t.Fatalf("task=%+v sent parts=%+v", task, adapter.lastMessage.Parts)
	}
	file := adapter.lastMessage.Parts[2]
	if file.ObjectID != "" || file.Filename != "source.txt" || file.MediaType != "text/plain" {
		t.Fatalf("materialized part=%+v", file)
	}
	decoded, err := base64.StdEncoding.DecodeString(file.BytesBase64)
	if err != nil || string(decoded) != "shared artifact" {
		t.Fatalf("materialized file bytes=%q err=%v", decoded, err)
	}
	if _, err := service.SubmitTask(context.Background(), "tenant-b", SubmitTaskInput{AgentID: "agent-1", Parts: []core.Part{{Kind: core.PartFile, ObjectID: object.ID}}}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-tenant Artifact input error=%v", err)
	}
}

func TestNormalizeMessagePartsRejectsAmbiguousFilesAndAcceptsDataOnly(t *testing.T) {
	parts, err := NormalizeMessageParts("", []core.Part{{Kind: core.PartData, Data: map[string]any{"ok": true}}})
	if err != nil || len(parts) != 1 || parts[0].Kind != core.PartData {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	if _, err := NormalizeMessageParts("", []core.Part{{Kind: core.PartFile, URI: "https://example.test/file", ObjectID: "object-1"}}); err == nil {
		t.Fatal("ambiguous file selectors were accepted")
	}
	if _, err := NormalizeMessageParts("", []core.Part{{Kind: core.PartFile, URI: "http://example.test/file"}}); err == nil {
		t.Fatal("non-HTTPS file URI was accepted")
	}
}
