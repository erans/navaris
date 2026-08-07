package webui

import (
	"net"
	"net/http"
	"strings"
)

// isTrustedPeer reports whether remoteAddr's host part is contained in any
// of the trusted CIDRs. An empty trusted list trusts nobody; an unparseable
// address is never trusted.
func isTrustedPeer(remoteAddr string, trusted []*net.IPNet) bool {
	if len(trusted) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, cidr := range trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the caller's IP for rate-limit bucketing.
//
// X-Forwarded-For is honored only when the operator configured trusted
// proxies and the direct peer is one of them; then the first hop is used
// and only if it is a syntactically valid IP. In every other case the
// direct peer IP is used. This is fail-closed: with no trusted proxies
// configured, XFF is ignored entirely (untrusted clients cannot steer
// their own rate-limit bucket, and junk header values cannot become
// unbounded map keys).
func (h *Handlers) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if isTrustedPeer(r.RemoteAddr, h.cfg.TrustedProxies) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := xff
			if i := strings.IndexByte(xff, ','); i >= 0 {
				first = xff[:i]
			}
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}
