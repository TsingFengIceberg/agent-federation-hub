package trust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
)

const (
	TokenExchangeGrantType       = "urn:ietf:params:oauth:grant-type:token-exchange"
	AccessTokenType              = "urn:ietf:params:oauth:token-type:access_token"
	TokenExchangeReferencePrefix = "exchange:"
)

type SecretResolver interface {
	ValidateReference(string) error
	Resolve(context.Context, string) (string, error)
}

type ExchangeProfile struct {
	Endpoint           string   `json:"endpoint"`
	Audience           string   `json:"audience"`
	Resource           string   `json:"resource,omitempty"`
	Scopes             []string `json:"scopes,omitempty"`
	SubjectTokenRef    string   `json:"subjectTokenRef"`
	SubjectTokenType   string   `json:"subjectTokenType,omitempty"`
	ActorTokenRef      string   `json:"actorTokenRef,omitempty"`
	ActorTokenType     string   `json:"actorTokenType,omitempty"`
	ClientID           string   `json:"clientId,omitempty"`
	ClientSecretRef    string   `json:"clientSecretRef,omitempty"`
	RequestedTokenType string   `json:"requestedTokenType,omitempty"`
}

type ExchangeRequest struct {
	Profile      ExchangeProfile
	SubjectToken string
	ActorToken   string
	ClientSecret string
}

type ExchangeResult struct {
	AccessToken     string
	IssuedTokenType string
	TokenType       string
	Scopes          []string
	ExpiresAt       time.Time
}

type TokenExchangeProvider struct {
	Base        SecretResolver
	Profiles    map[string]ExchangeProfile
	Client      *http.Client
	URLPolicy   netpolicy.Policy
	Now         func() time.Time
	ExpirySkew  time.Duration
	MaxLifetime time.Duration

	mu    sync.Mutex
	cache map[string]ExchangeResult
}

func NewTokenExchangeProvider(base SecretResolver, profiles map[string]ExchangeProfile) *TokenExchangeProvider {
	clone := make(map[string]ExchangeProfile, len(profiles))
	for name, profile := range profiles {
		profile.Scopes = append([]string(nil), profile.Scopes...)
		clone[name] = profile
	}
	return &TokenExchangeProvider{
		Base: base, Profiles: clone, URLPolicy: netpolicy.HTTPSOnlyPolicy(),
		ExpirySkew: 30 * time.Second, MaxLifetime: time.Hour, cache: make(map[string]ExchangeResult),
	}
}

func (p *TokenExchangeProvider) ValidateReference(reference string) error {
	name, exchange := strings.CutPrefix(reference, TokenExchangeReferencePrefix)
	if !exchange {
		if p.Base == nil {
			return errors.New("base secret provider is unavailable")
		}
		return p.Base.ValidateReference(reference)
	}
	profile, ok := p.Profiles[name]
	if !ok || name == "" {
		return errors.New("token exchange profile is not configured")
	}
	if profile.Endpoint == "" || profile.SubjectTokenRef == "" || profile.Audience == "" {
		return errors.New("token exchange profile requires endpoint, audience, and subject token reference")
	}
	if _, err := p.URLPolicy.ValidateURL(profile.Endpoint); err != nil {
		return fmt.Errorf("token exchange endpoint: %w", err)
	}
	for _, secretReference := range []string{profile.SubjectTokenRef, profile.ActorTokenRef, profile.ClientSecretRef} {
		if secretReference == "" {
			continue
		}
		if p.Base == nil {
			return errors.New("base secret provider is unavailable")
		}
		if err := p.Base.ValidateReference(secretReference); err != nil {
			return fmt.Errorf("token exchange secret reference: %w", err)
		}
	}
	return nil
}

func (p *TokenExchangeProvider) Resolve(ctx context.Context, reference string) (string, error) {
	name, exchange := strings.CutPrefix(reference, TokenExchangeReferencePrefix)
	if !exchange {
		if p.Base == nil {
			return "", errors.New("base secret provider is unavailable")
		}
		return p.Base.Resolve(ctx, reference)
	}
	if err := p.ValidateReference(reference); err != nil {
		return "", err
	}
	now := p.now()
	p.mu.Lock()
	if cached, ok := p.cache[name]; ok && now.Add(p.expirySkew()).Before(cached.ExpiresAt) {
		p.mu.Unlock()
		return cached.AccessToken, nil
	}
	p.mu.Unlock()
	profile := p.Profiles[name]
	subjectToken, err := p.Base.Resolve(ctx, profile.SubjectTokenRef)
	if err != nil {
		return "", errors.New("token exchange subject credential is unavailable")
	}
	actorToken, clientSecret := "", ""
	if profile.ActorTokenRef != "" {
		actorToken, err = p.Base.Resolve(ctx, profile.ActorTokenRef)
		if err != nil {
			return "", errors.New("token exchange actor credential is unavailable")
		}
	}
	if profile.ClientSecretRef != "" {
		clientSecret, err = p.Base.Resolve(ctx, profile.ClientSecretRef)
		if err != nil {
			return "", errors.New("token exchange client credential is unavailable")
		}
	}
	result, err := p.Exchange(ctx, ExchangeRequest{
		Profile: profile, SubjectToken: subjectToken, ActorToken: actorToken, ClientSecret: clientSecret,
	})
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.cache[name] = result
	p.mu.Unlock()
	return result.AccessToken, nil
}

func (p *TokenExchangeProvider) Exchange(ctx context.Context, request ExchangeRequest) (ExchangeResult, error) {
	profile := request.Profile
	if request.SubjectToken == "" || profile.Audience == "" {
		return ExchangeResult{}, errors.New("token exchange subject token and audience are required")
	}
	if _, err := p.URLPolicy.ValidateURL(profile.Endpoint); err != nil {
		return ExchangeResult{}, fmt.Errorf("token exchange endpoint: %w", err)
	}
	subjectType := profile.SubjectTokenType
	if subjectType == "" {
		subjectType = AccessTokenType
	}
	requestedType := profile.RequestedTokenType
	if requestedType == "" {
		requestedType = AccessTokenType
	}
	form := url.Values{
		"grant_type":           []string{TokenExchangeGrantType},
		"subject_token":        []string{request.SubjectToken},
		"subject_token_type":   []string{subjectType},
		"requested_token_type": []string{requestedType},
		"audience":             []string{profile.Audience},
	}
	if profile.Resource != "" {
		form.Set("resource", profile.Resource)
	}
	if len(profile.Scopes) > 0 {
		form.Set("scope", strings.Join(profile.Scopes, " "))
	}
	if request.ActorToken != "" {
		actorType := profile.ActorTokenType
		if actorType == "" {
			actorType = AccessTokenType
		}
		form.Set("actor_token", request.ActorToken)
		form.Set("actor_token_type", actorType)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, profile.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ExchangeResult{}, errors.New("create token exchange request")
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("Accept", "application/json")
	if profile.ClientID != "" {
		httpRequest.SetBasicAuth(profile.ClientID, request.ClientSecret)
	}
	client := p.Client
	if client == nil {
		policy := p.URLPolicy
		policy.MaxRedirects = -1
		client = netpolicy.NewHTTPClient(10*time.Second, nil, policy)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return ExchangeResult{}, errors.New("token exchange request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return ExchangeResult{}, fmt.Errorf("token exchange endpoint returned HTTP %d", response.StatusCode)
	}
	var document struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		TokenType       string `json:"token_type"`
		ExpiresIn       int64  `json:"expires_in"`
		Scope           string `json:"scope"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return ExchangeResult{}, errors.New("token exchange response is invalid")
	}
	if document.AccessToken == "" || document.IssuedTokenType == "" || document.TokenType == "" || document.ExpiresIn <= 0 {
		return ExchangeResult{}, errors.New("token exchange response is incomplete")
	}
	maximum := p.MaxLifetime
	if maximum <= 0 {
		maximum = time.Hour
	}
	if document.IssuedTokenType != requestedType || !strings.EqualFold(document.TokenType, "Bearer") ||
		time.Duration(document.ExpiresIn)*time.Second > maximum {
		return ExchangeResult{}, errors.New("token exchange response violates the requested token contract")
	}
	returnedScopes := strings.Fields(document.Scope)
	if !scopeSubset(returnedScopes, profile.Scopes) {
		return ExchangeResult{}, errors.New("token exchange response contains unrequested scopes")
	}
	return ExchangeResult{
		AccessToken: document.AccessToken, IssuedTokenType: document.IssuedTokenType,
		TokenType: document.TokenType, Scopes: returnedScopes,
		ExpiresAt: p.now().Add(time.Duration(document.ExpiresIn) * time.Second),
	}, nil
}

func scopeSubset(returned, requested []string) bool {
	allowed := make(map[string]struct{}, len(requested))
	for _, scope := range requested {
		allowed[scope] = struct{}{}
	}
	for _, scope := range returned {
		if _, ok := allowed[scope]; !ok {
			return false
		}
	}
	return true
}

func (p *TokenExchangeProvider) Invalidate(profile string) {
	p.mu.Lock()
	delete(p.cache, profile)
	p.mu.Unlock()
}

func (p *TokenExchangeProvider) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *TokenExchangeProvider) expirySkew() time.Duration {
	if p.ExpirySkew > 0 {
		return p.ExpirySkew
	}
	return 30 * time.Second
}
