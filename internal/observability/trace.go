package observability

import "context"

// Span is intentionally a small OpenTelemetry-compatible seam. An OTel
// adapter can implement it without making the Hub core depend on a vendor SDK.
type Span interface {
	End(error)
	SetAttribute(string, string)
}

type Tracer interface {
	Start(context.Context, string, map[string]string) (context.Context, Span)
}

type noopTracer struct{}
type noopSpan struct{}

func NoopTracer() Tracer { return noopTracer{} }
func (noopTracer) Start(ctx context.Context, _ string, _ map[string]string) (context.Context, Span) {
	return ctx, noopSpan{}
}
func (noopSpan) End(error)                   {}
func (noopSpan) SetAttribute(string, string) {}

type correlationKey struct{}

// WithCorrelation stores only safe identifiers used to join Hub and Provider
// traces. It must not be populated with prompts, model names, or credentials.
func WithCorrelation(ctx context.Context, values map[string]string) context.Context {
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return context.WithValue(ctx, correlationKey{}, copyValues)
}

func Correlation(ctx context.Context) map[string]string {
	values, _ := ctx.Value(correlationKey{}).(map[string]string)
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return copyValues
}
