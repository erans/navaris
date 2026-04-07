package webui

import (
	"strings"
	"testing"
	"time"
)

func TestSessionSignVerifyRoundTrip(t *testing.T) {
	key := []byte("unit-test-key-please-ignore")
	signer := NewSigner(key)
	val, err := signer.Sign(time.Now(), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(val, ".") {
		t.Fatalf("signed value missing separator: %q", val)
	}
	iat, exp, err := signer.Verify(val)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if iat.IsZero() || exp.IsZero() {
		t.Fatalf("iat/exp zero: iat=%v exp=%v", iat, exp)
	}
}

func TestSessionVerifyTamperedSignatureFails(t *testing.T) {
	signer := NewSigner([]byte("k"))
	val, _ := signer.Sign(time.Now(), time.Now().Add(time.Hour))
	parts := strings.SplitN(val, ".", 2)
	bad := parts[0] + "." + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, _, err := signer.Verify(bad); err == nil {
		t.Fatal("expected verify to fail with tampered signature")
	}
}

func TestSessionVerifyWrongKeyFails(t *testing.T) {
	val, _ := NewSigner([]byte("key-a")).Sign(time.Now(), time.Now().Add(time.Hour))
	if _, _, err := NewSigner([]byte("key-b")).Verify(val); err == nil {
		t.Fatal("expected verify to fail with wrong key")
	}
}

func TestSessionVerifyExpiredFails(t *testing.T) {
	signer := NewSigner([]byte("k"))
	val, _ := signer.Sign(time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if _, _, err := signer.Verify(val); err == nil {
		t.Fatal("expected verify to fail on expired cookie")
	}
}

func TestSessionVerifyMalformedFails(t *testing.T) {
	signer := NewSigner([]byte("k"))
	cases := []string{"", "no-dot", "only.one.dot.extra.parts.too.many", ".emptyleft", "emptyright."}
	for _, c := range cases {
		if _, _, err := signer.Verify(c); err == nil {
			t.Errorf("expected error for input %q", c)
		}
	}
}
