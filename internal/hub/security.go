package hub

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrInvalidPushCredential = errors.New("invalid Push credential")
	ErrPushTaskMismatch      = errors.New("Push task does not match callback task")
	ErrPushInboxUnavailable  = errors.New("durable Push inbox is unavailable")
)

func validateHTTPURL(raw string, publicOnly bool) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.User != nil || parsed.Hostname() == "" {
		return errors.New("URL must have a host and no user information")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if publicOnly && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal")) {
		return fmt.Errorf("public URL host %q is reserved for private use", hostname)
	}
	if publicOnly && parsed.Scheme != "https" {
		return errors.New("public URL must use HTTPS")
	}
	if !publicOnly && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL must use HTTP or HTTPS")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && publicOnly && !isPublicIP(ip) {
		return fmt.Errorf("URL IP %s is not public", ip)
	}
	return nil
}

func validateAgentEndpoint(binding, raw string, publicOnly bool) error {
	if strings.EqualFold(strings.ReplaceAll(binding, "_", ""), "GRPC") {
		value := strings.TrimSpace(raw)
		if value == "" || strings.Contains(value, "@") {
			return errors.New("gRPC endpoint must be non-empty and cannot contain user information")
		}
		parsed, err := url.Parse(value)
		if err != nil {
			return err
		}
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return validateHTTPURL(value, publicOnly)
		}
		if publicOnly {
			return errors.New("public gRPC endpoint must use an HTTPS authority")
		}
		// Local gRPC fixtures commonly use passthrough:///host:port or a bare
		// host:port target. They are accepted only with the explicit private URL
		// development switch.
		if strings.HasPrefix(value, "passthrough:///") || strings.HasPrefix(value, "dns:///") {
			return nil
		}
		if _, _, err := net.SplitHostPort(value); err == nil {
			return nil
		}
		return errors.New("gRPC endpoint must be an HTTPS URL or host:port target")
	}
	return validateHTTPURL(raw, publicOnly)
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}
