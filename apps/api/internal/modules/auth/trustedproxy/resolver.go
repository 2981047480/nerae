// Package trustedproxy resolves a source address only when an explicitly
// configured private/local proxy is the immediate HTTP peer.
package trustedproxy

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

const SourceIPHeader = "X-Nerae-Trusted-Source-IP"

var ErrUntrustedSource = errors.New("trusted source unavailable")

var permittedProxyNetworks = [...]netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// Resolver requires an explicit allowlist of private/local immediate proxies.
// It trusts exactly one dedicated header from those peers and never parses a
// forwarding chain. The proxy must overwrite, not append to, that header.
type Resolver struct {
	trusted []netip.Prefix
}

func NewResolver(trustedProxyPrefixes []netip.Prefix) (*Resolver, error) {
	if len(trustedProxyPrefixes) == 0 || len(trustedProxyPrefixes) > 16 {
		return nil, ErrUntrustedSource
	}
	trusted := make([]netip.Prefix, 0, len(trustedProxyPrefixes))
	seen := make(map[netip.Prefix]struct{}, len(trustedProxyPrefixes))
	for _, prefix := range trustedProxyPrefixes {
		if !prefix.IsValid() || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() || prefix != prefix.Masked() ||
			!privateLocalPrefix(prefix) {
			return nil, ErrUntrustedSource
		}
		if _, exists := seen[prefix]; exists {
			return nil, ErrUntrustedSource
		}
		seen[prefix] = struct{}{}
		trusted = append(trusted, prefix)
	}
	return &Resolver{trusted: trusted}, nil
}

func privateLocalPrefix(candidate netip.Prefix) bool {
	for _, allowed := range permittedProxyNetworks {
		if candidate.Addr().Is4() != allowed.Addr().Is4() || candidate.Bits() < allowed.Bits() {
			continue
		}
		if allowed.Contains(candidate.Addr()) {
			return true
		}
	}
	return false
}

func (r *Resolver) ResolveTrustedSourceIP(request *http.Request) (netip.Addr, error) {
	if r == nil || request == nil || request.Header == nil {
		return netip.Addr{}, ErrUntrustedSource
	}
	peer, err := remoteAddress(request.RemoteAddr)
	if err != nil || !r.trustedPeer(peer) {
		return netip.Addr{}, ErrUntrustedSource
	}
	value, ok := oneHeaderValue(request.Header, SourceIPHeader)
	if !ok || strings.ContainsAny(value, ", \t\r\n\x00") {
		return netip.Addr{}, ErrUntrustedSource
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, ErrUntrustedSource
	}
	address = address.Unmap()
	if !validClientAddress(address) {
		return netip.Addr{}, ErrUntrustedSource
	}
	return address, nil
}

func remoteAddress(value string) (netip.Addr, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return netip.Addr{}, ErrUntrustedSource
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || port == "" || len(port) > 5 || portNumber < 1 || portNumber > 65535 {
		return netip.Addr{}, ErrUntrustedSource
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return netip.Addr{}, ErrUntrustedSource
		}
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, ErrUntrustedSource
	}
	address = address.Unmap()
	if !validClientAddress(address) {
		return netip.Addr{}, ErrUntrustedSource
	}
	return address, nil
}

func validClientAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsMulticast() && address.Zone() == "" &&
		(address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast())
}

func (r *Resolver) trustedPeer(peer netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

func oneHeaderValue(headers http.Header, name string) (string, bool) {
	var values []string
	count := 0
	for key, candidate := range headers {
		if strings.EqualFold(key, name) {
			count++
			values = candidate
		}
	}
	if count != 1 || len(values) != 1 {
		return "", false
	}
	return values[0], true
}
