package webui

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

// CookieName is the cookie name used for session state.
const CookieName = "navaris_ui_session"

// Config parameterises the UI handlers. Construct it once in main and pass
// to NewHandlers. All durations with zero values fall back to defaults.
type Config struct {
	Password     string
	SessionKey   []byte
	SessionTTL   time.Duration
	LoginDelay   time.Duration
	RateCapacity float64
	RateRefill   float64
	RateInterval time.Duration
}

// Handlers holds the UI endpoints.
type Handlers struct {
	cfg    Config
	signer *Signer
	rl     *rateLimiter
}

// NewHandlers builds the UI handlers from Config. If RateCapacity is zero,
// defaults of 5/5/1-minute are applied; likewise LoginDelay defaults to
// 200ms.
func NewHandlers(cfg Config) *Handlers {
	if cfg.RateCapacity == 0 {
		cfg.RateCapacity = 5
	}
	if cfg.RateRefill == 0 {
		cfg.RateRefill = 5
	}
	if cfg.RateInterval == 0 {
		cfg.RateInterval = time.Minute
	}
	// LoginDelay defaults to 200ms unless explicitly overridden.
	if cfg.LoginDelay == 0 {
		cfg.LoginDelay = 200 * time.Millisecond
	}
	return &Handlers{
		cfg:    cfg,
		signer: NewSigner(cfg.SessionKey),
		rl:     newRateLimiter(cfg.RateCapacity, cfg.RateRefill, cfg.RateInterval),
	}
}

// Signer exposes the cookie signer so middleware can verify cookies.
func (h *Handlers) Signer() *Signer { return h.signer }

type loginBody struct {
	Password string `json:"password"`
}

// Login validates the password, sleeps the fixed delay, and on success sets
// a signed session cookie. On failure, returns 401 and consumes one token
// from the rate limiter bucket. 429 when the bucket is empty.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	// Rate limit check happens first — 429 is returned without even touching
	// the password comparison.
	clientIP := extractClientIP(r)
	if !h.rl.consume(clientIP) {
		writeJSON(w, 429, map[string]any{"error": "rate_limited", "retry_after": 60})
		return
	}

	var body loginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad_request"})
		return
	}

	// Fixed delay on every login attempt that gets as far as the password
	// comparison — success and failure alike. Note that a 400 (bad JSON)
	// returns immediately and so runs faster; this is a minor timing
	// channel that distinguishes "malformed body" from "wrong password"
	// but leaks nothing about the password itself.
	time.Sleep(h.cfg.LoginDelay)

	// Constant-time comparison.
	ok := subtle.ConstantTimeCompare([]byte(body.Password), []byte(h.cfg.Password)) == 1
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}

	// Refund the token on success — the rate limit only penalises failures.
	h.rl.refund(clientIP)

	now := time.Now()
	ttl := h.cfg.SessionTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	signed, err := h.signer.Sign(now, now.Add(ttl))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "internal_error"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Secure:   r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, 200, map[string]any{"authenticated": true})
}

// Logout clears the session cookie. The spec (Routes and Auth table line
// "POST /ui/logout") requires a valid cookie — an anonymous POST returns
// 401 without mutating any state. An invalid cookie is treated the same as
// no cookie at all.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	if _, _, err := h.signer.Verify(c.Value); err != nil {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, 200, map[string]any{"authenticated": false})
}

// Me reports whether the current request has a valid session cookie.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		writeJSON(w, 200, map[string]any{"authenticated": false})
		return
	}
	if _, _, err := h.signer.Verify(c.Value); err != nil {
		writeJSON(w, 200, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": true})
}

// extractClientIP returns the caller's IP for rate-limit bucketing. It
// trusts the first entry of X-Forwarded-For if present, which means it is
// ONLY safe when navarisd sits behind a reverse proxy that overwrites or
// strips inbound XFF headers. A directly-exposed instance lets clients
// pick their own rate-limit bucket by setting this header.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
