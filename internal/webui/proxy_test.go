package webui

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newReq(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestClientIPTurnsDownXFFWhenNoTrustedProxies(t *testing.T) {
	h := NewHandlers(Config{})
	r := newReq("192.0.2.1:1234", "203.0.113.9")
	if got := h.clientIP(r); got != "192.0.2.1" {
		t.Fatalf("clientIP = %q, want peer 192.0.2.1 (XFF must be ignored)", got)
	}
}

func TestClientIPUntrustedPeerCannotSpoof(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("192.0.2.1:1234", "203.0.113.9")
	if got := h.clientIP(r); got != "192.0.2.1" {
		t.Fatalf("clientIP = %q, want peer 192.0.2.1 for untrusted peer", got)
	}
}

func TestClientIPTrustedPeerHonorsFirstXFFHop(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("10.1.2.3:4321", "203.0.113.9, 10.9.9.9")
	if got := h.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want first XFF hop 203.0.113.9", got)
	}
}

func TestClientIPRejectsJunkXFF(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("10.1.2.3:4321", strings.Repeat("A", 500))
	if got := h.clientIP(r); got != "10.1.2.3" {
		t.Fatalf("clientIP = %q, want peer 10.1.2.3 for junk XFF", got)
	}
}

func TestIsTrustedPeer(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8", "192.168.1.5/32")
	cases := map[string]bool{"10.1.2.3": true, "192.168.1.5": true, "192.168.1.6": false, "bogus": false}
	for in, want := range cases {
		if got := isTrustedPeer(in, trusted); got != want {
			t.Errorf("isTrustedPeer(%q) = %v, want %v", in, got, want)
		}
	}
	if isTrustedPeer("10.1.2.3", nil) {
		t.Error("empty trusted list must trust nobody")
	}
}
