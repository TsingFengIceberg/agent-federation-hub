package a2afederation

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// ParseBindingProfiles converts the operator-facing comma-separated binding
// list into an ordered A2A profile set. The order is the preference used when
// an Agent Card advertises more than one interface.
func ParseBindingProfiles(value string) ([]BindingProfile, error) {
	var profiles []BindingProfile
	for _, raw := range strings.Split(value, ",") {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		profile := BindingProfile{ProtocolVersion: string(a2a.Version), StreamTransport: "SSE"}
		switch strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), "+", "_") {
		case "JSONRPC", "JSON_RPC":
			profile.Binding = a2a.TransportProtocolJSONRPC
		case "HTTP_JSON", "HTTPJSON":
			profile.Binding = a2a.TransportProtocolHTTPJSON
		case "GRPC":
			profile.Binding = a2a.TransportProtocolGRPC
			profile.StreamTransport = "SERVER_STREAMING"
		default:
			return nil, fmt.Errorf("unsupported A2A binding profile %q", raw)
		}
		if err := profile.Validate(); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("at least one A2A binding profile is required")
	}
	return profiles, nil
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
	if strings.TrimSpace(p.ProtocolVersion) == "" {
		return fmt.Errorf("A2A protocol version is required")
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
			if endpoint != nil && string(endpoint.ProtocolVersion) == profile.ProtocolVersion &&
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
