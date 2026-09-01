package access

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type policyRoundTripper func(*http.Request) (*http.Response, error)

func (f policyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPolicyDocumentLoadsRoleAndTenantRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	content := `{"version":1,"roles":{"operator":["tasks:read","agents:read"]},"tenantActions":{"tenant-a":{"task.read":"tasks:read"}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := LoadPolicyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewPolicyAuthorizer(document)
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{Subject: "operator-1", TenantID: "tenant-a", Roles: []string{"operator"}}
	if err := authorizer.Authorize(context.Background(), principal, Request{Action: ActionTaskRead}); err != nil {
		t.Fatalf("role authorization failed: %v", err)
	}
}

func TestPolicyDocumentRejectsUnknownActionAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"actions":{"unknown.action":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicyFile(path); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown action error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicyFile(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version error=%v", err)
	}
}

func TestRepositoryAccessPolicyExampleRemainsValid(t *testing.T) {
	if _, err := LoadPolicyFile("../../access_policy.example.json"); err != nil {
		t.Fatalf("load access_policy.example.json: %v", err)
	}
}

type staticBearer string

func (value staticBearer) Resolve(context.Context, string) (string, error) {
	return string(value), nil
}

func TestHTTPAuthorizerAllowsStructuredDecisionWithoutLeakingCredential(t *testing.T) {
	authorizer := &HTTPAuthorizer{
		Endpoint: "https://policy.example/v1/decide", Bearer: staticBearer("policy-secret"),
		BearerReference: "POLICY_TOKEN",
		Client: &http.Client{Transport: policyRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer policy-secret" {
				t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
			}
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), "policy-secret") || !strings.Contains(string(body), `"tenantId":"tenant-a"`) {
				t.Fatalf("policy input=%s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"allow":true,"decisionId":"decision-1"}`)),
			}, nil
		})},
	}
	err := authorizer.Authorize(context.Background(), Principal{Subject: "user", TenantID: "tenant-a"}, Request{Action: ActionTaskRead, ResourceID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAuthorizerFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		response string
		status   int
		err      error
	}{
		{name: "http endpoint", endpoint: "http://policy.example", status: http.StatusOK, response: `{"allow":true}`},
		{name: "denied", endpoint: "https://policy.example", status: http.StatusOK, response: `{"allow":false}`},
		{name: "invalid", endpoint: "https://policy.example", status: http.StatusOK, response: `{"allow":true,"unknown":1}`},
		{name: "upstream error", endpoint: "https://policy.example", err: errors.New("unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &HTTPAuthorizer{
				Endpoint: test.endpoint,
				Client: &http.Client{Transport: policyRoundTripper(func(*http.Request) (*http.Response, error) {
					if test.err != nil {
						return nil, test.err
					}
					return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.response))}, nil
				})},
			}
			if err := authorizer.Authorize(context.Background(), Principal{Subject: "u", TenantID: "t"}, Request{Action: ActionTaskRead}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
