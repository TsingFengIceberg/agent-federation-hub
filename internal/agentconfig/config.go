// Package agentconfig loads operator-owned remote Agent registrations.
//
// The file is intentionally separate from model configuration. It declares
// local policy and credential references; the remote Agent Card remains the
// authority for the Agent's discovered endpoint and capabilities.
package agentconfig

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
	"go.yaml.in/yaml/v3"
)

const CurrentSchemaVersion = 1

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type File struct {
	SchemaVersion int            `yaml:"schema_version"`
	Defaults      Defaults       `yaml:"defaults"`
	Agents        []Registration `yaml:"agents"`
}

type Defaults struct {
	Protocol  ProtocolPolicy  `yaml:"protocol"`
	Discovery DiscoveryPolicy `yaml:"discovery"`
	Execution ExecutionPolicy `yaml:"execution"`
	Limits    LimitsPolicy    `yaml:"limits"`
}

type Registration struct {
	ID            string            `yaml:"id"`
	TenantID      string            `yaml:"tenant_id"`
	DisplayName   string            `yaml:"display_name"`
	Enabled       bool              `yaml:"enabled"`
	CardURL       string            `yaml:"card_url"`
	Protocol      ProtocolPolicy    `yaml:"protocol"`
	Discovery     DiscoveryPolicy   `yaml:"discovery"`
	CredentialEnv map[string]string `yaml:"credential_env"`
	Execution     ExecutionPolicy   `yaml:"execution"`
	Limits        LimitsPolicy      `yaml:"limits"`
	Routing       RoutingPolicy     `yaml:"routing"`
	Metadata      map[string]string `yaml:"metadata"`
}

type ProtocolPolicy struct {
	Kind             string           `yaml:"kind"`
	Profiles         []BindingProfile `yaml:"profiles"`
	RequiredProfiles []BindingProfile `yaml:"required_profiles"`
}

type BindingProfile struct {
	ProtocolVersion string `yaml:"protocol_version"`
	Binding         string `yaml:"binding"`
	StreamTransport string `yaml:"stream_transport"`
}

type DiscoveryPolicy struct {
	RefreshIntervalSeconds int             `yaml:"refresh_interval_seconds"`
	MaxCardBytes           int64           `yaml:"max_card_bytes"`
	RequireHTTPS           bool            `yaml:"require_https"`
	AllowPrivateURLs       bool            `yaml:"allow_private_urls"`
	RequiredCapabilities   map[string]bool `yaml:"required_capabilities"`
	RequiredSkills         []string        `yaml:"required_skills"`
	AllowedSkills          []string        `yaml:"allowed_skills"`
}

type ExecutionPolicy struct {
	RemoteTimeoutSeconds int  `yaml:"remote_timeout_seconds"`
	TaskTimeoutSeconds   int  `yaml:"task_timeout_seconds"`
	MaxRetries           int  `yaml:"max_retries"`
	CancelGraceSeconds   int  `yaml:"cancel_grace_seconds"`
	Reconnect            bool `yaml:"reconnect"`
}

type LimitsPolicy struct {
	MaxConcurrentTasks int   `yaml:"max_concurrent_tasks"`
	MaxArtifactBytes   int64 `yaml:"max_artifact_bytes"`
}

type RoutingPolicy struct {
	Tags     []string `yaml:"tags"`
	Priority int      `yaml:"priority"`
}

func LoadFile(path string) (File, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read Agent configuration %q: %w", path, err)
	}
	var file File
	decoder := yaml.NewDecoder(strings.NewReader(string(encoded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return File{}, fmt.Errorf("parse Agent configuration %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return File{}, fmt.Errorf("parse Agent configuration %q: multiple YAML documents are not supported", path)
		}
		return File{}, fmt.Errorf("parse Agent configuration %q: %w", path, err)
	}
	if err := file.Validate(); err != nil {
		return File{}, fmt.Errorf("validate Agent configuration %q: %w", path, err)
	}
	return file, nil
}

func (f File) Validate() error {
	if f.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("schema_version must be %d", CurrentSchemaVersion)
	}
	if err := f.Defaults.Validate("defaults", false); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(f.Agents))
	for index, agent := range f.Agents {
		if err := agent.validate(index, f.Defaults); err != nil {
			return err
		}
		if _, exists := seen[agent.ID]; exists {
			return fmt.Errorf("agents[%d].id %q is duplicated", index, agent.ID)
		}
		seen[agent.ID] = struct{}{}
	}
	return nil
}

func (p ProtocolPolicy) Validate(field string, required bool) error {
	if p.Kind != "" && p.Kind != "a2a" {
		return fmt.Errorf("%s.kind must be a2a", field)
	}
	if len(p.Profiles) > 0 && len(p.RequiredProfiles) > 0 {
		return fmt.Errorf("%s must use either profiles or required_profiles, not both", field)
	}
	if required && len(p.Profiles) == 0 && len(p.RequiredProfiles) == 0 {
		return fmt.Errorf("%s.profiles must not be empty", field)
	}
	profiles := append(append([]BindingProfile(nil), p.Profiles...), p.RequiredProfiles...)
	for i, profile := range profiles {
		if strings.TrimSpace(profile.ProtocolVersion) == "" {
			return fmt.Errorf("%s.profiles[%d].protocol_version is required", field, i)
		}
		binding := strings.ToUpper(strings.TrimSpace(profile.Binding))
		if binding != "JSONRPC" && binding != "HTTP_JSON" {
			return fmt.Errorf("%s.profiles[%d].binding must be JSONRPC or HTTP_JSON", field, i)
		}
		if profile.StreamTransport != "" && strings.ToUpper(profile.StreamTransport) != "SSE" {
			return fmt.Errorf("%s.profiles[%d].stream_transport must be SSE", field, i)
		}
	}
	return nil
}

func (p DiscoveryPolicy) Validate(field string, required bool) error {
	if p.RefreshIntervalSeconds < 0 || p.MaxCardBytes < 0 {
		return fmt.Errorf("%s limits must not be negative", field)
	}
	if required && p.MaxCardBytes == 0 {
		return fmt.Errorf("%s.max_card_bytes must be positive", field)
	}
	for name := range p.RequiredCapabilities {
		if name != "streaming" && name != "push_notifications" {
			return fmt.Errorf("%s.required_capabilities contains unsupported capability %q", field, name)
		}
	}
	for i, skill := range p.AllowedSkills {
		if strings.TrimSpace(skill) == "" {
			return fmt.Errorf("%s.allowed_skills[%d] must not be empty", field, i)
		}
	}
	for i, skill := range p.RequiredSkills {
		if strings.TrimSpace(skill) == "" {
			return fmt.Errorf("%s.required_skills[%d] must not be empty", field, i)
		}
	}
	return nil
}

func (p ExecutionPolicy) Validate(field string, required bool) error {
	if required && p.RemoteTimeoutSeconds <= 0 {
		return fmt.Errorf("%s.remote_timeout_seconds must be positive", field)
	}
	if p.RemoteTimeoutSeconds < 0 || p.TaskTimeoutSeconds < 0 || p.MaxRetries < 0 || p.CancelGraceSeconds < 0 {
		return fmt.Errorf("%s values must not be negative", field)
	}
	if p.TaskTimeoutSeconds != 0 && p.TaskTimeoutSeconds < p.RemoteTimeoutSeconds {
		return fmt.Errorf("%s.task_timeout_seconds must be >= remote_timeout_seconds", field)
	}
	return nil
}

func (p LimitsPolicy) Validate(field string, required bool) error {
	if required && p.MaxConcurrentTasks <= 0 {
		return fmt.Errorf("%s.max_concurrent_tasks must be positive", field)
	}
	if p.MaxConcurrentTasks < 0 || p.MaxArtifactBytes < 0 {
		return fmt.Errorf("%s values must not be negative", field)
	}
	return nil
}

func (d Defaults) Validate(field string, required bool) error {
	if err := d.Protocol.Validate(field+".protocol", required); err != nil {
		return err
	}
	if err := d.Discovery.Validate(field+".discovery", required); err != nil {
		return err
	}
	if err := d.Execution.Validate(field+".execution", required); err != nil {
		return err
	}
	return d.Limits.Validate(field+".limits", required)
}

func (r Registration) validate(index int, defaults Defaults) error {
	prefix := fmt.Sprintf("agents[%d]", index)
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%s.id is required", prefix)
	}
	if strings.TrimSpace(r.TenantID) == "" {
		return fmt.Errorf("%s.tenant_id is required", prefix)
	}
	if strings.TrimSpace(r.CardURL) == "" {
		return fmt.Errorf("%s.card_url is required", prefix)
	}
	parsed, err := url.Parse(r.CardURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s.card_url must be an absolute HTTP(S) URL without user info", prefix)
	}
	discovery := mergeDiscovery(defaults.Discovery, r.Discovery)
	if discovery.RequireHTTPS && parsed.Scheme != "https" && !discovery.AllowPrivateURLs {
		return fmt.Errorf("%s.card_url must use HTTPS", prefix)
	}
	if err := r.Protocol.Validate(prefix+".protocol", false); err != nil {
		return err
	}
	profiles := r.Protocol.Profiles
	if len(profiles) == 0 {
		profiles = r.Protocol.RequiredProfiles
	}
	if len(profiles) == 0 {
		profiles = defaults.Protocol.Profiles
	}
	if err := (ProtocolPolicy{Profiles: profiles}).Validate(prefix+".protocol", true); err != nil {
		return err
	}
	if err := discovery.Validate(prefix+".discovery", false); err != nil {
		return err
	}
	execution := mergeExecution(defaults.Execution, r.Execution)
	if err := execution.Validate(prefix+".execution", true); err != nil {
		return err
	}
	limits := mergeLimits(defaults.Limits, r.Limits)
	if err := limits.Validate(prefix+".limits", true); err != nil {
		return err
	}
	for scheme, reference := range r.CredentialEnv {
		if strings.TrimSpace(scheme) == "" || !envNamePattern.MatchString(reference) {
			return fmt.Errorf("%s.credential_env[%q] must reference a valid environment variable", prefix, scheme)
		}
	}
	return nil
}

func mergeDiscovery(base, override DiscoveryPolicy) DiscoveryPolicy {
	result := override
	if result.RefreshIntervalSeconds == 0 {
		result.RefreshIntervalSeconds = base.RefreshIntervalSeconds
	}
	if result.MaxCardBytes == 0 {
		result.MaxCardBytes = base.MaxCardBytes
	}
	if !result.RequireHTTPS {
		result.RequireHTTPS = base.RequireHTTPS
	}
	if !result.AllowPrivateURLs {
		result.AllowPrivateURLs = base.AllowPrivateURLs
	}
	if result.RequiredCapabilities == nil {
		result.RequiredCapabilities = base.RequiredCapabilities
	}
	if result.RequiredSkills == nil {
		result.RequiredSkills = base.RequiredSkills
	}
	if result.AllowedSkills == nil {
		result.AllowedSkills = base.AllowedSkills
	}
	return result
}

func mergeExecution(base, override ExecutionPolicy) ExecutionPolicy {
	result := override
	if result.RemoteTimeoutSeconds == 0 {
		result.RemoteTimeoutSeconds = base.RemoteTimeoutSeconds
	}
	if result.TaskTimeoutSeconds == 0 {
		result.TaskTimeoutSeconds = base.TaskTimeoutSeconds
	}
	if result.MaxRetries == 0 {
		result.MaxRetries = base.MaxRetries
	}
	if result.CancelGraceSeconds == 0 {
		result.CancelGraceSeconds = base.CancelGraceSeconds
	}
	if !result.Reconnect {
		result.Reconnect = base.Reconnect
	}
	return result
}

func mergeLimits(base, override LimitsPolicy) LimitsPolicy {
	result := override
	if result.MaxConcurrentTasks == 0 {
		result.MaxConcurrentTasks = base.MaxConcurrentTasks
	}
	if result.MaxArtifactBytes == 0 {
		result.MaxArtifactBytes = base.MaxArtifactBytes
	}
	return result
}

func (r Registration) Profiles(defaults Defaults) []BindingProfile {
	if len(r.Protocol.Profiles) > 0 {
		return append([]BindingProfile(nil), r.Protocol.Profiles...)
	}
	if len(r.Protocol.RequiredProfiles) > 0 {
		return append([]BindingProfile(nil), r.Protocol.RequiredProfiles...)
	}
	return append([]BindingProfile(nil), defaults.Protocol.Profiles...)
}

func (r Registration) RegistrationPolicy(defaults Defaults) hub.AgentRegistrationPolicy {
	profiles := r.Profiles(defaults)
	policy := hub.AgentRegistrationPolicy{}
	if len(profiles) > 0 {
		policy.RequiredProtocolVersion = profiles[0].ProtocolVersion
		policy.RequiredProtocolBinding = strings.ToUpper(profiles[0].Binding)
		policy.RequiredStreamTransport = strings.ToUpper(profiles[0].StreamTransport)
	}
	discovery := mergeDiscovery(defaults.Discovery, r.Discovery)
	policy.RequireStreaming = discovery.RequiredCapabilities["streaming"]
	policy.RequirePushNotifications = discovery.RequiredCapabilities["push_notifications"]
	policy.RequiredSkills = append([]string(nil), discovery.RequiredSkills...)
	return policy
}

func (r Registration) AllowsPrivateURLs(defaults Defaults) bool {
	return mergeDiscovery(defaults.Discovery, r.Discovery).AllowPrivateURLs
}

func (f File) EnabledAgents() []Registration {
	result := make([]Registration, 0)
	for _, agent := range f.Agents {
		if agent.Enabled {
			result = append(result, agent)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

var ErrNoEnabledAgents = errors.New("no enabled Agents configured")
