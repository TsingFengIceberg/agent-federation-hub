package a2afederation

import (
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/a2aproject/a2a-go/v2/a2a"
)

func FuzzDecodePushNeverPanics(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":{"id":"task-1","status":{"state":"completed"}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":"x","error":{"code":-32600,"message":"invalid"}}`))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = DecodePush(payload)
	})
}

func FuzzCanonicalAgentCardNeverPanics(f *testing.F) {
	f.Add("provider", "1.0", "https://agent.example/a2a")
	f.Add("", "", "not a URL")
	f.Fuzz(func(t *testing.T, name, version, endpoint string) {
		card := &a2a.AgentCard{
			Name: name, Version: version,
			SupportedInterfaces: []*a2a.AgentInterface{{
				URL: endpoint, ProtocolBinding: a2a.TransportProtocolJSONRPC,
				ProtocolVersion: a2a.Version,
			}},
		}
		_, _ = CanonicalAgentCard(card)
	})
}

func FuzzExtensionAdmissionNeverPanics(f *testing.F) {
	f.Add("https://example.com/ext", "https://example.com/other")
	f.Add("javascript:bad", "")
	f.Fuzz(func(t *testing.T, first, second string) {
		_, _ = requestedExtensions(core.Agent{Extensions: []string{first}}, []string{first, second})
	})
}
