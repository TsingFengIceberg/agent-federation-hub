package a2afederation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// ProtocolVersionsCompatible reports whether two A2A protocol versions may
// be used together. A2A profiles negotiate the major/minor contract; a patch
// release is an implementation correction and must not make an otherwise
// compatible interface undiscoverable. Malformed versions are never treated
// as compatible.
func ProtocolVersionsCompatible(advertised, required string) bool {
	advertisedMajor, advertisedMinor, ok := parseProtocolVersion(advertised)
	if !ok {
		return false
	}
	requiredMajor, requiredMinor, ok := parseProtocolVersion(required)
	return ok && advertisedMajor == requiredMajor && advertisedMinor == requiredMinor
}

// ValidateProtocolVersion validates the version shape accepted by the Hub.
// A2A currently uses a major.minor version with an optional numeric patch.
func ValidateProtocolVersion(value string) error {
	if _, _, ok := parseProtocolVersion(value); !ok {
		return fmt.Errorf("invalid A2A protocol version %q; expected major.minor[.patch]", value)
	}
	return nil
}

// CanonicalProtocolVersion returns the Major.Minor value that must be sent in
// the A2A-Version service parameter. Patch values may be present in an
// AgentCard for implementation bookkeeping, but the wire protocol negotiates
// only major and minor versions.
func CanonicalProtocolVersion(value string) (string, error) {
	major, minor, ok := parseProtocolVersion(value)
	if !ok {
		return "", fmt.Errorf("invalid A2A protocol version %q; expected major.minor[.patch]", value)
	}
	return fmt.Sprintf("%d.%d", major, minor), nil
}

func parseProtocolVersion(value string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, false
	}
	parsed := make([]int, len(parts))
	for index, part := range parts {
		if part == "" || (len(part) > 1 && (part[0] == '+' || part[0] == '0')) {
			return 0, 0, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return 0, 0, false
		}
		parsed[index] = number
	}
	return parsed[0], parsed[1], true
}

// ParseBindingProfiles converts the operator-facing comma-separated binding
// list into an ordered A2A profile set. The order is the preference used when
// an Agent Card advertises more than one interface.
func ParseBindingProfiles(value string) ([]BindingProfile, error) {
	var profiles []BindingProfile
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(value, ",") {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		profile := BindingProfile{ProtocolVersion: string(a2a.Version), StreamTransport: "SSE"}
		normalized := strings.NewReplacer("-", "_", "+", "_", "/", "_").Replace(name)
		switch normalized {
		case "JSONRPC", "JSON_RPC":
			profile.Binding = a2a.TransportProtocolJSONRPC
		case "HTTP_JSON", "HTTPJSON":
			profile.Binding = a2a.TransportProtocolHTTPJSON
		case "GRPC":
			profile.Binding = a2a.TransportProtocolGRPC
			profile.StreamTransport = "SERVER_STREAMING"
		case "A2A_V1_JSONRPC_SSE", "V1_JSONRPC_SSE":
			profile.Binding = a2a.TransportProtocolJSONRPC
		case "A2A_V1_HTTP_JSON_SSE", "A2A_V1_HTTPJSON_SSE", "V1_HTTP_JSON_SSE", "V1_HTTPJSON_SSE":
			profile.Binding = a2a.TransportProtocolHTTPJSON
		case "A2A_V1_GRPC", "V1_GRPC":
			profile.Binding = a2a.TransportProtocolGRPC
			profile.StreamTransport = "SERVER_STREAMING"
		default:
			return nil, fmt.Errorf("unsupported A2A binding profile %q", raw)
		}
		if err := profile.Validate(); err != nil {
			return nil, err
		}
		profileID := ProfileName(profile)
		if _, exists := seen[profileID]; exists {
			return nil, fmt.Errorf("duplicate A2A binding profile %q", raw)
		}
		seen[profileID] = struct{}{}
		profiles = append(profiles, profile)
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("at least one A2A binding profile is required")
	}
	return profiles, nil
}

// ProfileName is the stable operator/configuration identifier for a binding
// profile. It is intentionally independent of SDK enum spelling.
func ProfileName(profile BindingProfile) string {
	version, err := CanonicalProtocolVersion(profile.ProtocolVersion)
	if err != nil {
		return ""
	}
	parts := strings.Split(version, ".")
	versionName := version
	if len(parts) == 2 {
		versionName = parts[0]
	}
	binding := strings.ToLower(strings.ReplaceAll(string(normalizeBinding(string(profile.Binding))), "+", "-"))
	stream := strings.ToLower(strings.TrimSpace(profile.StreamTransport))
	if stream == "server_streaming" || stream == "grpc" {
		return "a2a-v" + versionName + "-" + binding
	}
	return "a2a-v" + versionName + "-" + binding + "-sse"
}

// KnownBindingProfiles returns the explicitly supported v1 profiles in
// preference order. A caller must still opt into more than the default
// JSON-RPC profile when constructing an Adapter.
func KnownBindingProfiles() map[string]BindingProfile {
	profiles := []BindingProfile{InitialBindingProfile, {
		ProtocolVersion: string(a2a.Version), Binding: a2a.TransportProtocolHTTPJSON, StreamTransport: "SSE",
	}, GRPCBindingProfile}
	result := make(map[string]BindingProfile, len(profiles))
	for _, profile := range profiles {
		result[ProfileName(profile)] = profile
	}
	return result
}

// BindingProfile is the explicit wire contract the Hub is willing to use for
// a remote Agent. Profiles are ordered by preference at the adapter boundary;
// no SDK default is allowed to silently widen the supported contract.
type BindingProfile struct {
	ProtocolVersion string
	Binding         a2a.TransportProtocol
	StreamTransport string
}

var InitialBindingProfile = BindingProfile{
	ProtocolVersion: string(a2a.Version),
	Binding:         a2a.TransportProtocolJSONRPC,
	StreamTransport: "SSE",
}

// GRPCBindingProfile is opt-in because gRPC endpoints require a separately
// qualified TLS/name-resolution profile. It is kept explicit rather than
// silently widening the default JSON-RPC+SSE contract.
var GRPCBindingProfile = BindingProfile{
	ProtocolVersion: string(a2a.Version),
	Binding:         a2a.TransportProtocolGRPC,
	StreamTransport: "SERVER_STREAMING",
}

func (p BindingProfile) Validate() error {
	if err := ValidateProtocolVersion(p.ProtocolVersion); err != nil {
		return err
	}
	if p.Binding != a2a.TransportProtocolJSONRPC && p.Binding != a2a.TransportProtocolHTTPJSON && p.Binding != a2a.TransportProtocolGRPC {
		return fmt.Errorf("unsupported A2A binding %q", p.Binding)
	}
	stream := strings.ToUpper(strings.TrimSpace(p.StreamTransport))
	if p.Binding == a2a.TransportProtocolGRPC {
		if stream != "SERVER_STREAMING" && stream != "GRPC" {
			return fmt.Errorf("gRPC stream transport must be SERVER_STREAMING")
		}
	} else if stream != "SSE" {
		return fmt.Errorf("unsupported stream transport %q", p.StreamTransport)
	}
	return nil
}

func selectEndpointForProfiles(card *a2a.AgentCard, profiles []BindingProfile) (*a2a.AgentInterface, BindingProfile, error) {
	if card == nil {
		return nil, BindingProfile{}, fmt.Errorf("Agent Card is required")
	}
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			return nil, BindingProfile{}, err
		}
		for _, endpoint := range card.SupportedInterfaces {
			if endpoint != nil && ProtocolVersionsCompatible(string(endpoint.ProtocolVersion), profile.ProtocolVersion) &&
				normalizeBinding(string(endpoint.ProtocolBinding)) == normalizeBinding(string(profile.Binding)) {
				return endpoint, profile, nil
			}
		}
	}
	return nil, BindingProfile{}, fmt.Errorf("Agent Card has no supported configured A2A binding profile")
}

// normalizeBinding accepts the spelling emitted by the Python A2A SDK
// (JSON_RPC) as well as the canonical Go SDK value (JSONRPC). Binding aliases
// are normalized only at the interoperability boundary; stored profiles and
// internal routing continue to use the Go SDK constants.
func normalizeBinding(value string) a2a.TransportProtocol {
	compact := strings.ToUpper(strings.NewReplacer("_", "", "-", "", "+", "").Replace(strings.TrimSpace(value)))
	switch compact {
	case "JSONRPC":
		return a2a.TransportProtocolJSONRPC
	case "HTTPJSON":
		return a2a.TransportProtocolHTTPJSON
	default:
		return a2a.TransportProtocol(value)
	}
}

func normalizeCardBindings(card *a2a.AgentCard) {
	if card == nil {
		return
	}
	for _, endpoint := range card.SupportedInterfaces {
		if endpoint != nil {
			endpoint.ProtocolBinding = normalizeBinding(string(endpoint.ProtocolBinding))
		}
	}
}

func cloneCard(card *a2a.AgentCard) (*a2a.AgentCard, error) {
	if card == nil {
		return nil, fmt.Errorf("Agent Card is required")
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("clone Agent Card: %w", err)
	}
	var clone a2a.AgentCard
	if err := json.Unmarshal(payload, &clone); err != nil {
		return nil, fmt.Errorf("clone Agent Card: %w", err)
	}
	return &clone, nil
}
