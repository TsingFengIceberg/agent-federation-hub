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
	AllowedPorts map[string]struct{}
	MaxRedirects int
}

func HTTPSOnlyPolicy() Policy {
	return Policy{AllowedPorts: map[string]struct{}{"443": {}}, MaxRedirects: 3}
}

func (p Policy) ValidateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("URL must use HTTPS, include a host, and omit user information")
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
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
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
		},
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

func HostPort(parsed *url.URL) string {
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}

func PortNumber(parsed *url.URL) int {
	port := parsed.Port()
	if port == "" {
		return 443
	}
	value, _ := strconv.Atoi(port)
	return value
}
