package agentconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Runtime owns the currently accepted Agent configuration snapshot. Reload
// validates and applies a complete candidate before publishing it, so a bad
// file or a failed reconciliation never replaces the last known-good state.
type Runtime struct {
	path   string
	apply  func(context.Context, File, bool) error
	report func(error)

	reloadMu sync.Mutex
	mu       sync.RWMutex
	current  File
	digest   [32]byte
	accepted bool
}

// NewRuntime loads the initial snapshot and applies it before returning. The
// apply callback receives the candidate and whether it is the first snapshot.
func NewRuntime(ctx context.Context, path string, apply func(context.Context, File, bool) error) (*Runtime, error) {
	if path == "" {
		return nil, errors.New("Agent configuration path is required")
	}
	runtime := &Runtime{path: path, apply: apply}
	if _, err := runtime.Reload(ctx); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) SetErrorReporter(report func(error)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.report = report
	r.mu.Unlock()
}

// Current returns a defensive copy of the accepted snapshot.
func (r *Runtime) Current() (File, bool) {
	if r == nil {
		return File{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.accepted {
		return File{}, false
	}
	return cloneFile(r.current), true
}

// Reload reads and validates the configured file. It returns true only when a
// new snapshot was accepted and applied. Errors leave the current snapshot
// unchanged and are returned to the caller for explicit handling.
func (r *Runtime) Reload(ctx context.Context) (bool, error) {
	if r == nil || r.path == "" {
		return false, errors.New("Agent configuration runtime is not configured")
	}
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	encoded, err := os.ReadFile(r.path)
	if err != nil {
		return false, fmt.Errorf("read Agent configuration %q: %w", r.path, err)
	}
	digest := sha256.Sum256(encoded)
	r.mu.RLock()
	same := r.accepted && digest == r.digest
	r.mu.RUnlock()
	if same {
		return false, nil
	}
	candidate, err := LoadFile(r.path)
	if err != nil {
		return false, err
	}
	r.mu.RLock()
	first := !r.accepted
	r.mu.RUnlock()
	if r.apply != nil {
		if err := r.apply(ctx, candidate, first); err != nil {
			return false, fmt.Errorf("apply Agent configuration %q: %w", r.path, err)
		}
	}
	r.mu.Lock()
	r.current = cloneFile(candidate)
	r.digest = digest
	r.accepted = true
	r.mu.Unlock()
	return true, nil
}

// Watch polls the file at interval. Reload errors are reported and do not stop
// the watcher; cancellation is the only normal termination condition.
func (r *Runtime) Watch(ctx context.Context, interval time.Duration) {
	if r == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.Reload(ctx); err != nil {
				r.mu.RLock()
				report := r.report
				r.mu.RUnlock()
				if report != nil {
					report(err)
				}
			}
		}
	}
}

func cloneFile(file File) File {
	clone := file
	clone.Agents = append([]Registration(nil), file.Agents...)
	clone.Defaults.Protocol.Profiles = append([]BindingProfile(nil), file.Defaults.Protocol.Profiles...)
	clone.Defaults.Protocol.RequiredProfiles = append([]BindingProfile(nil), file.Defaults.Protocol.RequiredProfiles...)
	clone.Defaults.Discovery.RequiredSkills = append([]string(nil), file.Defaults.Discovery.RequiredSkills...)
	clone.Defaults.Discovery.AllowedSkills = append([]string(nil), file.Defaults.Discovery.AllowedSkills...)
	if file.Defaults.Discovery.RequiredCapabilities != nil {
		clone.Defaults.Discovery.RequiredCapabilities = map[string]bool{}
		for key, value := range file.Defaults.Discovery.RequiredCapabilities {
			clone.Defaults.Discovery.RequiredCapabilities[key] = value
		}
	}
	for index := range clone.Agents {
		clone.Agents[index].Protocol.Profiles = append([]BindingProfile(nil), file.Agents[index].Protocol.Profiles...)
		clone.Agents[index].Protocol.RequiredProfiles = append([]BindingProfile(nil), file.Agents[index].Protocol.RequiredProfiles...)
		clone.Agents[index].Discovery.RequiredSkills = append([]string(nil), file.Agents[index].Discovery.RequiredSkills...)
		clone.Agents[index].Discovery.AllowedSkills = append([]string(nil), file.Agents[index].Discovery.AllowedSkills...)
		if file.Agents[index].Discovery.RequiredCapabilities != nil {
			clone.Agents[index].Discovery.RequiredCapabilities = map[string]bool{}
			for key, value := range file.Agents[index].Discovery.RequiredCapabilities {
				clone.Agents[index].Discovery.RequiredCapabilities[key] = value
			}
		}
		clone.Agents[index].CredentialEnv = cloneStringMap(file.Agents[index].CredentialEnv)
		clone.Agents[index].Metadata = cloneStringMap(file.Agents[index].Metadata)
		clone.Agents[index].Routing.Tags = append([]string(nil), file.Agents[index].Routing.Tags...)
	}
	return clone
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
