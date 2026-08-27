package netpolicy

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
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
