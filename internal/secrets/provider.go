package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
)

var (
	ErrReferenceDenied = errors.New("secret reference is not allowed")
	ErrSecretNotFound  = errors.New("secret is not available")
	envNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Provider interface {
	ValidateReference(string) error
	Resolve(context.Context, string) (string, error)
}

type EnvProvider struct {
	allowed map[string]struct{}
	lookup  func(string) (string, bool)
}

func NewEnvProvider(allowed map[string]struct{}) *EnvProvider {
	clone := make(map[string]struct{}, len(allowed))
	for name := range allowed {
		clone[name] = struct{}{}
	}
	return &EnvProvider{allowed: clone, lookup: os.LookupEnv}
}

func NewEnvProviderForTest(allowed map[string]string) *EnvProvider {
	references := make(map[string]struct{}, len(allowed))
	for name := range allowed {
		references[name] = struct{}{}
	}
	return &EnvProvider{
		allowed: references,
		lookup: func(name string) (string, bool) {
			value, ok := allowed[name]
			return value, ok
		},
	}
}

func (p *EnvProvider) ValidateReference(reference string) error {
	if !envNamePattern.MatchString(reference) {
		return fmt.Errorf("%w: invalid environment reference", ErrReferenceDenied)
	}
	if _, ok := p.allowed[reference]; !ok {
		return fmt.Errorf("%w: reference is outside the operator allowlist", ErrReferenceDenied)
	}
	return nil
}

func (p *EnvProvider) Resolve(_ context.Context, reference string) (string, error) {
	if err := p.ValidateReference(reference); err != nil {
		return "", err
	}
	value, ok := p.lookup(reference)
	if !ok || value == "" {
		return "", fmt.Errorf("%w: configured reference has no value", ErrSecretNotFound)
	}
	return value, nil
}
