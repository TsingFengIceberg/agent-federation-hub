package artifact

import (
	"context"
	"errors"
	"io"
)

var (
	ErrUnavailable = errors.New("artifact content is unavailable")
	ErrPolicy      = errors.New("artifact content violates policy")
)

type ObjectInfo struct {
	SizeBytes int64
	MediaType string
}

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Open(context.Context, string) (io.ReadCloser, ObjectInfo, error)
	Delete(context.Context, string) error
}

// HealthStore is optional so test doubles and provider-specific stores can be
// used without pretending to expose infrastructure health. Production object
// stores should implement it so readiness can stop traffic before uploads fail.
type HealthStore interface {
	Health(context.Context) error
}
