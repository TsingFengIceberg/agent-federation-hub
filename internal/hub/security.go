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

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}
