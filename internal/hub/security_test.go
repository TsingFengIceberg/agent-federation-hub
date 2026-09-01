package hub

import "testing"

func TestPublicURLPolicyRejectsUnsafeTargets(t *testing.T) {
	for _, raw := range []string{
		"http://agent.example/card.json",
		"https://127.0.0.1/card.json",
		"https://10.0.0.1/card.json",
		"https://169.254.169.254/latest/meta-data",
		"https://service.internal/card.json",
		"https://localhost/card.json",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := validateHTTPURL(raw, true); err == nil {
				t.Fatalf("unsafe URL accepted: %s", raw)
			}
		})
	}
	if err := validateHTTPURL("https://agent.example/card.json", true); err != nil {
		t.Fatalf("public HTTPS URL rejected: %v", err)
	}
}

func TestValidateAgentCardURLMatchesRegistrationPolicy(t *testing.T) {
	if err := ValidateAgentCardURL("https://127.0.0.1/card.json", false); err == nil {
		t.Fatal("private AgentCard target accepted outside development")
	}
	if err := ValidateAgentCardURL("http://127.0.0.1/card.json", true); err != nil {
		t.Fatalf("local development AgentCard target rejected: %v", err)
	}
}
