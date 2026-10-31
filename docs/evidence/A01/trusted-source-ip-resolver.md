# Trusted source IP resolver seam

Date: 2026-10-31

## Scope and behavior

Added the standalone standard-library package `apps/api/internal/modules/auth/trustedproxy`. `NewResolver` requires an explicit, duplicate-free allowlist of masked prefixes contained in private, loopback, link-local, or IPv6 ULA address ranges. The resolver trusts only an immediate peer inside that list and exactly one `X-Nerae-Trusted-Source-IP` value. It does not parse `Forwarded`, `X-Forwarded-For`, or a comma-separated proxy chain. It rejects public/unlisted peers, duplicate header spellings or values, malformed remote addresses, invalid or space-padded source values, zones, unspecified and multicast addresses. IPv4-mapped values are unmapped before returning. A valid private prefix can still be broad; deployment must choose the actual proxy's smallest practical trusted range.

The returned `netip.Addr` can be passed into the existing `transport.TrustedSourceIPResolver` interface. This package is not injected into a handler, production route, API process configuration, or deployment in this task.

## Trust assumptions and deployment limits

The immediate proxy CIDRs are trust configuration, not request data. The proxy must overwrite and sanitize `X-Nerae-Trusted-Source-IP`; this resolver cannot prove which process authored a header before it reached the trusted peer. The repository currently has no Caddy/Nginx config. `NERAE_HTTP_ADDR` accepts an explicit host:port but does not require loopback/private binding; `cmd/api/server.go` listens on that configured address. Therefore this implementation alone does not close access to a publicly reachable backend.

If the backend listener is reachable only from loopback, another process on the same machine can still connect and forge the dedicated header. Production assembly must restrict the listener to a proxy-only boundary (for example, a permission-controlled Unix socket, isolated network namespace, or authenticated private proxy link), configure the proxy to replace the dedicated header with its immediate client address, and configure only the proxy's actual peer CIDR. This work does not assume those changes exist.

## Verification

- `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test ./internal/modules/auth/trustedproxy`: passed.
- The tests cover configured and untrusted peer ranges, fake `X-Forwarded-For` without the dedicated header, duplicates, comma chains, malformed/zone addresses, IPv4-mapped normalization, and invalid trust-prefix configuration.
- No server, proxy, route, external address, or production data was used.
- Lead ran `go test -mod=readonly -race -count=1 ./internal/modules/auth/trustedproxy` using the local Go toolchain and offline caches after the final port and forwarding-header test changes: exit 0.
