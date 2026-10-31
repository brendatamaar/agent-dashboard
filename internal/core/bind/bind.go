package bind

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrPublicBindNotAllowed = errors.New("binding to public or wildcard address is forbidden without --insecure-allow-public-bind")
var ErrInvalidAddress = errors.New("invalid bind address format")

// ValidateBindAddress is a pure policy over literal addresses. DNS resolution
// belongs to the shell; names never imply that an address is on Tailscale.
func ValidateBindAddress(addr string, allowPublic bool) error {
	addr = strings.TrimSpace(addr)
	host := addr
	if strings.HasPrefix(addr, "[") {
		if end := strings.Index(addr, "]:"); end >= 0 {
			host = addr[1:end]
		}
	} else if strings.Count(addr, ":") == 1 {
		host = strings.SplitN(addr, ":", 2)[0]
	}
	if allowPublic {
		return nil
	}
	if strings.EqualFold(host, "localhost") || host == "::1" || host == "0:0:0:0:0:0:0:1" {
		return nil
	}
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		octets := [4]int{}
		valid := true
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 255 || strconv.Itoa(n) != p {
				valid = false
				break
			}
			octets[i] = n
		}
		if valid && (octets[0] == 127 || (octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127)) {
			return nil
		}
	}
	// Tailscale uses this fixed ULA /48; reject zones and invalid hex tails.
	lower := strings.ToLower(host)
	if strings.HasPrefix(lower, "fd7a:115c:a1e0:") && validIPv6(lower) {
		return nil
	}
	return fmt.Errorf("%w: %q is not a literal loopback or Tailscale address", ErrPublicBindNotAllowed, host)
}
func validIPv6(host string) bool {
	if strings.Count(host, "::") > 1 {
		return false
	}
	parts := strings.Split(host, ":")
	groups := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		if len(p) > 4 {
			return false
		}
		if _, err := strconv.ParseUint(p, 16, 16); err != nil {
			return false
		}
		groups++
	}
	if strings.Contains(host, "::") {
		return groups < 8
	}
	return groups == 8
}
