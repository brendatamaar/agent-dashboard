package bind

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	// ErrPublicBindNotAllowed is returned when an address resolves to a public or wildcard interface without explicit flag.
	ErrPublicBindNotAllowed = errors.New("binding to public or wildcard address is forbidden without --insecure-allow-public-bind")
	// ErrInvalidAddress is returned when the address format is invalid.
	ErrInvalidAddress = errors.New("invalid bind address format")
)

var (
	// Tailscale IPv4 CGNAT range: 100.64.0.0/10 (100.64.0.0 - 100.127.255.255)
	_, tailscaleIPv4Range, _ = net.ParseCIDR("100.64.0.0/10")
	// Tailscale IPv6 ULA range: fd7a:115c:a1e0::/48
	_, tailscaleIPv6Range, _ = net.ParseCIDR("fd7a:115c:a1e0::/48")
)

// IsTailscaleIP checks if an IP belongs to Tailscale's allocated address ranges.
func IsTailscaleIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return tailscaleIPv4Range.Contains(ip) || tailscaleIPv6Range.Contains(ip)
}

// IsLoopback checks if an IP is loopback (127.0.0.0/8 or ::1).
func IsLoopback(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// ValidateBindAddress verifies that the host in the address complies with the security bind policy.
// Allowed by default: loopback (127.0.0.1, ::1, localhost) and Tailscale addresses.
// Wildcards (0.0.0.0, ::, "") and non-Tailscale network interfaces require allowPublic = true.
func ValidateBindAddress(addr string, allowPublic bool) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		if !allowPublic {
			return fmt.Errorf("%w: empty bind address defaults to wildcard", ErrPublicBindNotAllowed)
		}
		return nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Could be host without port
		host = addr
	}

	host = strings.Trim(host, "[]")

	// Empty host or wildcard
	if host == "" || host == "0.0.0.0" || host == "::" {
		if !allowPublic {
			return fmt.Errorf("%w: %q", ErrPublicBindNotAllowed, host)
		}
		return nil
	}

	// Localhost is always allowed
	if strings.EqualFold(host, "localhost") {
		return nil
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if IsLoopback(ip) || IsTailscaleIP(ip) {
			return nil
		}
		if !allowPublic {
			return fmt.Errorf("%w: %q is not loopback or Tailscale", ErrPublicBindNotAllowed, host)
		}
		return nil
	}

	// Named host (e.g. tailscale magicDNS name like my-vps.ts.net)
	if strings.HasSuffix(strings.ToLower(host), ".ts.net") {
		return nil
	}

	if !allowPublic {
		return fmt.Errorf("%w: hostname %q is not recognized as loopback or Tailscale", ErrPublicBindNotAllowed, host)
	}

	return nil
}
