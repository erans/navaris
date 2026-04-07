package webui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signer produces and validates stateless HMAC-signed session values.
//
// Format: base64url(issued_at_unix|expires_at_unix).base64url(hmac_sha256(payload, key))
//
// Both halves are url-safe base64 without padding. Verify uses constant-time
// comparison on the signature.
type Signer struct {
	key []byte
}

// NewSigner constructs a Signer with the given HMAC key. Callers are
// responsible for generating or loading the key.
func NewSigner(key []byte) *Signer {
	return &Signer{key: key}
}

// Sign returns a signed string encoding issued-at and expires-at timestamps.
func (s *Signer) Sign(issuedAt, expiresAt time.Time) (string, error) {
	if s == nil || len(s.key) == 0 {
		return "", errors.New("webui: signer has no key")
	}
	payload := fmt.Sprintf("%d|%d", issuedAt.Unix(), expiresAt.Unix())
	encPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(encPayload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encPayload + "." + sig, nil
}

// Verify checks HMAC and expiry and returns the decoded timestamps.
func (s *Signer) Verify(value string) (issuedAt, expiresAt time.Time, err error) {
	if s == nil || len(s.key) == 0 {
		err = errors.New("webui: signer has no key")
		return
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		err = errors.New("webui: malformed session value")
		return
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, decodeErr := base64.RawURLEncoding.DecodeString(parts[1])
	if decodeErr != nil {
		err = errors.New("webui: malformed signature")
		return
	}
	if !hmac.Equal(want, got) {
		err = errors.New("webui: signature mismatch")
		return
	}
	payload, decodeErr := base64.RawURLEncoding.DecodeString(parts[0])
	if decodeErr != nil {
		err = errors.New("webui: malformed payload")
		return
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 2 {
		err = errors.New("webui: malformed payload fields")
		return
	}
	iat, e1 := strconv.ParseInt(fields[0], 10, 64)
	exp, e2 := strconv.ParseInt(fields[1], 10, 64)
	if e1 != nil || e2 != nil {
		err = errors.New("webui: malformed payload timestamps")
		return
	}
	issuedAt = time.Unix(iat, 0)
	expiresAt = time.Unix(exp, 0)
	if time.Now().After(expiresAt) {
		err = errors.New("webui: session expired")
		return
	}
	return
}
