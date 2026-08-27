package trust

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type exchangeRoundTripper func(*http.Request) (*http.Response, error)

func (function exchangeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type mapSecrets map[string]string

func (secrets mapSecrets) ValidateReference(reference string) error {
	if _, ok := secrets[reference]; !ok {
		return io.EOF
	}
	return nil
}

func (secrets mapSecrets) Resolve(_ context.Context, reference string) (string, error) {
	value, ok := secrets[reference]
	if !ok {
		return "", io.EOF
	}
	return value, nil
}

func TestTokenExchangeProviderBindsAudienceScopeActorAndCaches(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	calls := 0
	provider := NewTokenExchangeProvider(mapSecrets{
		"SUBJECT": "subject-secret", "ACTOR": "actor-secret", "CLIENT": "client-secret",
	}, map[string]ExchangeProfile{
		"partner": {
			Endpoint: "https://identity.partner.example/token", Audience: "https://agent.partner.example",
			Scopes: []string{"tasks.submit", "artifacts.read"}, SubjectTokenRef: "SUBJECT",
			ActorTokenRef: "ACTOR", ClientID: "hub-client", ClientSecretRef: "CLIENT",
		},
	})
	provider.Now = func() time.Time { return now }
	provider.Client = &http.Client{Transport: exchangeRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		for key, expected := range map[string]string{
			"grant_type": TokenExchangeGrantType, "subject_token": "subject-secret",
			"actor_token": "actor-secret", "audience": "https://agent.partner.example",
			"scope": "tasks.submit artifacts.read",
		} {
			if form.Get(key) != expected {
				t.Fatalf("%s=%q", key, form.Get(key))
			}
		}
		wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("hub-client:client-secret"))
		if request.Header.Get("Authorization") != wantBasic {
			t.Fatalf("client authentication=%q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"delegated-token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token",
				"token_type":"Bearer","expires_in":300,"scope":"tasks.submit artifacts.read"
			}`)),
		}, nil
	})}

	for range 2 {
		credential, err := provider.Resolve(context.Background(), "exchange:partner")
		if err != nil || credential != "delegated-token" {
			t.Fatalf("credential=%q err=%v", credential, err)
		}
	}
	if calls != 1 {
		t.Fatalf("token exchange calls=%d", calls)
	}
}

func TestTokenExchangeErrorsDoNotExposeCredentials(t *testing.T) {
	provider := NewTokenExchangeProvider(mapSecrets{"SUBJECT": "subject-secret"}, map[string]ExchangeProfile{
		"partner": {Endpoint: "https://identity.example/token", Audience: "agent", SubjectTokenRef: "SUBJECT"},
	})
	provider.Client = &http.Client{Transport: exchangeRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"error":"invalid_request","detail":"subject-secret"}`)),
		}, nil
	})}
	_, err := provider.Resolve(context.Background(), "exchange:partner")
	if err == nil || strings.Contains(err.Error(), "subject-secret") {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestTokenExchangeRejectsExpandedScopeAndUnexpectedTokenType(t *testing.T) {
	for name, response := range map[string]string{
		"expanded scope":  `{"access_token":"token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300,"scope":"tasks.submit admin"}`,
		"unexpected type": `{"access_token":"token","issued_token_type":"urn:ietf:params:oauth:token-type:refresh_token","token_type":"Bearer","expires_in":300,"scope":"tasks.submit"}`,
	} {
		t.Run(name, func(t *testing.T) {
			provider := NewTokenExchangeProvider(mapSecrets{}, nil)
			provider.Client = &http.Client{Transport: exchangeRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			_, err := provider.Exchange(context.Background(), ExchangeRequest{
				Profile: ExchangeProfile{
					Endpoint: "https://identity.example/token", Audience: "agent",
					Scopes: []string{"tasks.submit"},
				},
				SubjectToken: "subject",
			})
			if err == nil {
				t.Fatal("unsafe token exchange response was accepted")
			}
		})
	}
}
