package trustedproxy

import (
	"errors"
	"net/http"
	"net/netip"
	"testing"
)

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	resolver, err := NewResolver([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.20.0.0/16")})
	if err != nil {
		t.Fatal("NewResolver returned error")
	}
	return resolver
}

func request(peer, source string) *http.Request {
	r := &http.Request{RemoteAddr: peer, Header: make(http.Header)}
	if source != "" {
		r.Header.Set(SourceIPHeader, source)
	}
	return r
}

func TestResolveTrustedProxyDedicatedHeader(t *testing.T) {
	resolver := testResolver(t)
	got, err := resolver.ResolveTrustedSourceIP(request("127.0.0.1:41234", "203.0.113.17"))
	if err != nil || got != netip.MustParseAddr("203.0.113.17") {
		t.Fatalf("ResolveTrustedSourceIP() = %v, %v", got, err)
	}

	// A common, attacker-controlled forwarding header is never an input.
	spoof := request("203.0.113.9:41234", "")
	spoof.Header.Set("X-Forwarded-For", "198.51.100.9")
	if _, err := resolver.ResolveTrustedSourceIP(spoof); !errors.Is(err, ErrUntrustedSource) {
		t.Fatalf("X-Forwarded-For-only request error = %v", err)
	}
	withSpoofedForwarding := request("127.0.0.1:41234", "203.0.113.17")
	withSpoofedForwarding.Header.Set("X-Forwarded-For", "198.51.100.9")
	got, err = resolver.ResolveTrustedSourceIP(withSpoofedForwarding)
	if err != nil || got != netip.MustParseAddr("203.0.113.17") {
		t.Fatalf("standard forwarding header changed source IP to %v (%v)", got, err)
	}
}

func TestRejectsUntrustedImmediatePeersAndMalformedRemoteAddress(t *testing.T) {
	resolver := testResolver(t)
	for _, peer := range []string{
		"203.0.113.8:443", "192.0.2.2:1234", "10.21.0.1:80", "127.0.0.1", "127.0.0.1:0",
		"127.0.0.1:65536", "127.0.0.1:+1234", "127.0.0.1:abc", "[fe80::1%en0]:1234", "0.0.0.0:1234", "224.0.0.1:1234",
	} {
		t.Run(peer, func(t *testing.T) {
			if _, err := resolver.ResolveTrustedSourceIP(request(peer, "198.51.100.1")); !errors.Is(err, ErrUntrustedSource) {
				t.Fatalf("ResolveTrustedSourceIP() error = %v", err)
			}
		})
	}
}

func TestRejectsMissingRepeatedCommaZoneAndInvalidSourceHeaders(t *testing.T) {
	resolver := testResolver(t)
	for _, source := range []string{"", "203.0.113.1, 198.51.100.2", " 203.0.113.1", "203.0.113.1 ",
		"fe80::1%en0", "0.0.0.0", "224.0.0.1", "255.255.255.255", "host.example"} {
		t.Run(source, func(t *testing.T) {
			if _, err := resolver.ResolveTrustedSourceIP(request("127.0.0.1:1234", source)); !errors.Is(err, ErrUntrustedSource) {
				t.Fatalf("ResolveTrustedSourceIP() error = %v", err)
			}
		})
	}

	duplicate := request("127.0.0.1:1234", "203.0.113.1")
	duplicate.Header.Add(SourceIPHeader, "198.51.100.2")
	if _, err := resolver.ResolveTrustedSourceIP(duplicate); !errors.Is(err, ErrUntrustedSource) {
		t.Fatalf("duplicate header error = %v", err)
	}

	caseDuplicate := request("127.0.0.1:1234", "203.0.113.1")
	caseDuplicate.Header["x-nerae-trusted-source-ip"] = []string{"198.51.100.2"}
	if _, err := resolver.ResolveTrustedSourceIP(caseDuplicate); !errors.Is(err, ErrUntrustedSource) {
		t.Fatalf("case-variant duplicate header error = %v", err)
	}
}

func TestUnmapsIPv4MappedSource(t *testing.T) {
	resolver := testResolver(t)
	got, err := resolver.ResolveTrustedSourceIP(request("[::ffff:127.0.0.1]:1234", "::ffff:203.0.113.17"))
	if err != nil || got != netip.MustParseAddr("203.0.113.17") {
		t.Fatalf("mapped address = %v, %v", got, err)
	}
}

func TestNewResolverRequiresExplicitPrivateOrLocalPrefixes(t *testing.T) {
	for _, prefixes := range [][]netip.Prefix{
		nil,
		{netip.MustParsePrefix("0.0.0.0/0")},
		{netip.MustParsePrefix("203.0.113.0/24")},
		{netip.MustParsePrefix("10.0.0.0/7")}, // includes non-private 11/8
		{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")},
		{netip.MustParsePrefix("10.20.0.1/16")}, // host bits set
		{netip.MustParsePrefix("::ffff:127.0.0.0/104")},
	} {
		if _, err := NewResolver(prefixes); !errors.Is(err, ErrUntrustedSource) {
			t.Fatalf("NewResolver(%v) error = %v", prefixes, err)
		}
	}
	if _, err := NewResolver([]netip.Prefix{netip.MustParsePrefix("fc00::/7")}); err != nil {
		t.Fatalf("NewResolver(ULA) error = %v", err)
	}
}

func TestResolveRequiresExactlyOneCaseInsensitiveHeaderName(t *testing.T) {
	resolver := testResolver(t)
	r := request("10.20.1.2:2468", "198.51.100.3")
	got, err := resolver.ResolveTrustedSourceIP(r)
	if err != nil || got != netip.MustParseAddr("198.51.100.3") {
		t.Fatalf("private proxy resolution = %v, %v", got, err)
	}
	if _, err := resolver.ResolveTrustedSourceIP(nil); !errors.Is(err, ErrUntrustedSource) {
		t.Fatalf("nil request error = %v", err)
	}
}
