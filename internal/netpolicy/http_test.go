package netpolicy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type staticResolver map[string][]netip.Addr

func (r staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

func TestHTTPSPolicyRejectsPrivateTargetsAndUnsafePorts(t *testing.T) {
	policy := HTTPSOnlyPolicy()
	for _, raw := range []string{
		"http://example.com/file", "https://localhost/file", "https://127.0.0.1/file",
		"https://169.254.169.254/latest", "https://example.com:8443/file", "https://user@example.com/file",
	} {
		if _, err := policy.ValidateURL(raw); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	if _, err := policy.ValidateURL("https://example.com/file"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBaseURLRejectsQueryAndFragment(t *testing.T) {
	policy := HTTPSOnlyPolicy()
	for _, raw := range []string{"https://registry.example/path?tenant=tenant-a", "https://registry.example/path#fragment"} {
		if _, err := policy.ValidateBaseURL(raw); err == nil {
			t.Fatalf("base URL %q was accepted", raw)
		}
	}
	if _, err := policy.ValidateBaseURL("https://registry.example/path"); err != nil {
		t.Fatal(err)
	}
}

func TestSameOriginIgnoresPathButChecksAuthority(t *testing.T) {
	left, err := url.Parse("https://registry.example/bundle")
	if err != nil {
		t.Fatal(err)
	}
	right, err := url.Parse("https://registry.example/signature")
	if err != nil {
		t.Fatal(err)
	}
	if !SameOrigin(left, right) {
		t.Fatal("same HTTPS origin was rejected")
	}
	other, _ := url.Parse("https://other.example/signature")
	if SameOrigin(left, other) {
		t.Fatal("different HTTPS origin was accepted")
	}
}

func TestPublicAddressClassification(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "169.254.1.1", "100.64.0.1", "192.0.2.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "::1", "fc00::1", "2001:db8::1",
	} {
		if PublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("private address classified public: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if !PublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("public address rejected: %s", raw)
		}
	}
}

func TestRedirectPolicyCanDisableRedirects(t *testing.T) {
	policy := HTTPSOnlyPolicy()
	policy.MaxRedirects = -1
	client := NewHTTPClient(time.Second, staticResolver{}, policy)
	request, _ := http.NewRequest(http.MethodGet, "https://other.example/file", nil)
	if err := client.CheckRedirect(request, []*http.Request{{}}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error=%v", err)
	}
}

type staticRoundTripper struct{}

func (staticRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestWithURLPolicyChecksSchemeEvenForCustomTransport(t *testing.T) {
	client := WithURLPolicy(&http.Client{Transport: staticRoundTripper{}}, staticResolver{}, HTTPSOnlyPolicy())
	request, err := http.NewRequest(http.MethodGet, "http://public.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); err == nil {
		t.Fatal("custom transport bypassed HTTPS-only policy")
	}
}

func TestRestrictedDialContextRejectsPrivateDNSBeforeDial(t *testing.T) {
	policy := HTTPSOnlyPolicy()
	client := NewHTTPClient(time.Second, staticResolver{"agent.example": {netip.MustParseAddr("10.0.0.8")}}, policy)
	request, err := http.NewRequest(http.MethodGet, "https://agent.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	if err == nil || !strings.Contains(err.Error(), "resolved address") {
		t.Fatalf("private DNS result was not rejected: %v", err)
	}
}
