package secrets

import (
	"context"
	"errors"
	"testing"
)

func TestEnvProviderEnforcesReferenceAllowlistAndIsolation(t *testing.T) {
	provider := NewEnvProviderForTest(map[string]string{"ALLOWED_TOKEN": "secret-value"})
	value, err := provider.Resolve(context.Background(), "ALLOWED_TOKEN")
	if err != nil || value != "secret-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := provider.Resolve(context.Background(), "UNLISTED_TOKEN"); !errors.Is(err, ErrReferenceDenied) {
		t.Fatalf("unlisted reference error=%v", err)
	}
	if _, err := provider.Resolve(context.Background(), "../../secret"); !errors.Is(err, ErrReferenceDenied) {
		t.Fatalf("invalid reference error=%v", err)
	}
}
