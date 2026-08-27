package artifact

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestS3CompatibleObjectStore(t *testing.T) {
	endpoint := os.Getenv("AFH_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("AFH_TEST_S3_ENDPOINT is not configured")
	}
	configuration := S3Config{
		Endpoint: endpoint, Bucket: os.Getenv("AFH_TEST_S3_BUCKET"),
		AccessKeyID:     os.Getenv("AFH_TEST_S3_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("AFH_TEST_S3_SECRET_KEY"),
		Secure:          os.Getenv("AFH_TEST_S3_SECURE") == "true",
	}
	store, err := NewS3Store(configuration)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.client.MakeBucket(ctx, configuration.Bucket, minio.MakeBucketOptions{}); err != nil {
		exists, existsErr := store.client.BucketExists(ctx, configuration.Bucket)
		if existsErr != nil || !exists {
			t.Fatalf("create bucket: %v (exists error: %v)", err, existsErr)
		}
	}
	if err := store.Health(ctx); err != nil {
		t.Fatalf("S3 health: %v", err)
	}
	key := "aa/" + strings.Repeat("b", 64)
	payload := "S3-compatible Artifact"
	if err := store.Put(ctx, key, strings.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	reader, info, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(read) != payload || info.SizeBytes != int64(len(payload)) || info.MediaType != "text/plain" {
		t.Fatalf("payload=%q info=%+v", read, info)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Open(ctx, key); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted object open error=%v", err)
	}
}
