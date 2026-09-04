package netpolicy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	AllowPrivate bool
	// AllowHTTP is intended only for explicitly enabled local development. A
	// production outbound dependency must remain HTTPS even when its hostname
	// is otherwise public.
	AllowHTTP    bool
	AllowedPorts map[string]struct{}
	MaxRedirects int
}

func HTTPSOnlyPolicy() Policy {
	return Policy{AllowedPorts: map[string]struct{}{"443": {}}, MaxRedirects: 3}
}

// HTTPSBaseURLPolicy keeps HTTPS and public-host validation while allowing a
// deployment to expose a control-plane service on a non-default TLS port.
// Egress NetworkPolicy remains the enforcement point for the final port list.
func HTTPSBaseURLPolicy() Policy {
	policy := HTTPSOnlyPolicy()
	policy.AllowedPorts = nil
	return policy
}

func (p Policy) ValidateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "https" && !(p.AllowHTTP && parsed.Scheme == "http")) || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("URL must use HTTPS, or explicitly allowed HTTP, include a host, and omit user information")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	if len(p.AllowedPorts) > 0 {
		if _, ok := p.AllowedPorts[port]; !ok {
			return nil, fmt.Errorf("URL port %s is not allowed", port)
		}
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !p.AllowPrivate && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal")) {
		return nil, fmt.Errorf("URL host %q is reserved for private use", hostname)
	}
	if address, err := netip.ParseAddr(hostname); err == nil && !p.AllowPrivate && !PublicAddress(address) {
		return nil, fmt.Errorf("URL address %s is not public", address)
	}
	return parsed, nil
}

// ValidateBaseURL validates an operator-configured service endpoint. Unlike
// arbitrary resource URLs (for example, a presigned Artifact URL), control
// plane base endpoints must not carry a query or fragment: those components
// are not part of the service identity and can accidentally smuggle tenant or
// credential-routing state across retries and redirects.
func (p Policy) ValidateBaseURL(raw string) (*url.URL, error) {
	parsed, err := p.ValidateURL(raw)
	if err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("base URL must not contain a query or fragment")
	}
	return parsed, nil
}

func PublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return address.IsGlobalUnicast()
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func NewHTTPClient(timeout time.Duration, resolver Resolver, policy Policy) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: min(timeout, 10*time.Second), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       restrictedDialContext(dialer, resolver, policy),
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConns:      100,
		IdleConnTimeout:   90 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if _, err := policy.ValidateURL(request.URL.String()); err != nil {
			return err
		}
		maximum := policy.MaxRedirects
		if maximum < 0 {
			return http.ErrUseLastResponse
		}
		if maximum <= 0 {
			maximum = 3
		}
		if len(via) > maximum {
			return errors.New("redirect limit exceeded")
		}
		return nil
	}
	return client
}

// WithURLPolicy applies the same DNS rebinding and private-address checks to
// an already configured HTTP client. It preserves custom TLS roots and client
// certificates, which lets callers combine mTLS with the SSRF boundary.
func WithURLPolicy(client *http.Client, resolver Resolver, policy Policy) *http.Client {
	if client == nil {
		return NewHTTPClient(10*time.Second, resolver, policy)
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if httpTransport, ok := base.(*http.Transport); ok {
		transportClone := httpTransport.Clone()
		dialer := &net.Dialer{Timeout: min(clone.Timeout, 10*time.Second), KeepAlive: 30 * time.Second}
		transportClone.Proxy = nil
		transportClone.DialContext = restrictedDialContext(dialer, resolver, policy)
		// Keep the URL check outside the transport as well as the restricted
		// dialer.  The dialer protects DNS resolution, while the round tripper
		// rejects an unsafe scheme/authority before a custom transport can send
		// the request.
		clone.Transport = policyRoundTripper{base: transportClone, policy: policy}
	} else {
		clone.Transport = policyRoundTripper{base: base, policy: policy}
	}
	clone.CheckRedirect = restrictedRedirect(policy)
	return &clone
}

// RestrictedDialContext returns a gRPC-compatible dial function that applies
// the same DNS rebinding and private-address policy as HTTP clients. gRPC's
// resolver supplies an authority (for example, "agent.example:443") to this
// callback, so validation is repeated at connection time rather than relying
// only on the AgentCard URL checked during admission.
func RestrictedDialContext(dialer *net.Dialer, resolver Resolver, policy Policy) func(context.Context, string, string) (net.Conn, error) {
	return restrictedDialContext(dialer, resolver, policy)
}

func restrictedDialContext(dialer *net.Dialer, resolver Resolver, policy Policy) func(context.Context, string, string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		// The scheme is checked before RoundTrip. Here we validate the authority
		// and port again to cover custom transports and redirects.
		if _, err := policy.ValidateURL("https://" + net.JoinHostPort(host, port)); err != nil {
			return nil, err
		}
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("resolve %s: no addresses", host)
		}
		for _, candidate := range addresses {
			if !policy.AllowPrivate && !PublicAddress(candidate.Unmap()) {
				return nil, fmt.Errorf("resolved address %s is not public", candidate)
			}
		}
		var failures []error
		for _, candidate := range addresses {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			failures = append(failures, err)
		}
		return nil, errors.Join(failures...)
	}
}

type policyRoundTripper struct {
	base   http.RoundTripper
	policy Policy
}

func (r policyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("HTTP request URL is required")
	}
	if _, err := r.policy.ValidateURL(request.URL.String()); err != nil {
		return nil, err
	}
	return r.base.RoundTrip(request)
}

func restrictedRedirect(policy Policy) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if _, err := policy.ValidateURL(request.URL.String()); err != nil {
			return err
		}
		maximum := policy.MaxRedirects
		if maximum < 0 {
			return http.ErrUseLastResponse
		}
		if maximum <= 0 {
			maximum = 3
		}
		if len(via) > maximum {
			return errors.New("redirect limit exceeded")
		}
		return nil
	}
}

func HostPort(parsed *url.URL) string {
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}

// SameOrigin reports whether two absolute URLs address the same scheme,
// hostname, and effective port. It intentionally ignores paths and query
// components; callers use it when two endpoints share credentials but expose
// different resources on one control-plane origin.
func SameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	leftPort := left.Port()
	if leftPort == "" {
		leftPort = defaultPort(left.Scheme)
	}
	rightPort := right.Port()
	if rightPort == "" {
		rightPort = defaultPort(right.Scheme)
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(strings.TrimSuffix(left.Hostname(), "."), strings.TrimSuffix(right.Hostname(), ".")) &&
		leftPort == rightPort
}

func defaultPort(scheme string) string {
	if strings.EqualFold(scheme, "http") {
		return "80"
	}
	return "443"
}

func PortNumber(parsed *url.URL) int {
	port := parsed.Port()
	if port == "" {
		return 443
	}
	value, _ := strconv.Atoi(port)
	return value
}
