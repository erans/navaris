package webui

import (
	"strings"
	"testing"
	"time"
)

func TestSessionSignVerifyRoundTrip(t *testing.T) {
	key := []byte("unit-test-key-please-ignore")
	signer := NewSigner(key)
	wantIat := time.Now()
	wantExp := wantIat.Add(10 * time.Minute)
	val, err := signer.Sign(wantIat, wantExp)
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
	if diff := iat.Sub(wantIat); diff > time.Second || diff < -time.Second {
		t.Errorf("iat round-trip diff = %v, want < 1s (token stores unix seconds)", diff)
	}
	if diff := exp.Sub(wantExp); diff > time.Second || diff < -time.Second {
		t.Errorf("exp round-trip diff = %v, want < 1s", diff)
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

func TestSessionKeyIsCopiedOnConstruction(t *testing.T) {
	key := []byte("unit-test-key-please-ignore")
	signer := NewSigner(key)

	// Sign while the Signer still holds (what should be) its own copy.
	val, err := signer.Sign(time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// NOW zero the caller's buffer. If NewSigner did not copy the key,
	// signer.key is also zeroed and Verify will recompute the wrong HMAC
	// against the signature produced above with the real key.
	for i := range key {
		key[i] = 0
	}

	if _, _, err := signer.Verify(val); err != nil {
		t.Fatalf("verify after caller zeroed key: %v — Signer did not defensively copy the key", err)
	}
}
