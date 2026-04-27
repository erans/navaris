package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

// BoostHTTPHandler serves the in-sandbox HTTP API on connections that have
// already been bound to a single sandbox by the transport (FC vsock listener
// or Incus per-sandbox UDS). All requests on a given conn act as that sandbox.
//
// The handler doesn't run net/http.Server because the transport is inherently
// one-conn-per-listener-per-sandbox, and we want byte-level control over the
// response. It reads one request, writes one response, closes the conn.
type BoostHTTPHandler struct {
	boosts    *service.BoostService
	sandboxes domain.SandboxStore
	limiter   *RateLimiter
}

// NewBoostHTTPHandler constructs a BoostHTTPHandler with the given services
// and rate limiter.
func NewBoostHTTPHandler(boosts *service.BoostService, sandboxes domain.SandboxStore, limiter *RateLimiter) *BoostHTTPHandler {
	return &BoostHTTPHandler{boosts: boosts, sandboxes: sandboxes, limiter: limiter}
}

// Serve handles one request on conn, then closes conn. sandboxID is the
// implicit identity of the channel — every request on this conn acts as
// that sandbox.
func (h *BoostHTTPHandler) Serve(ctx context.Context, conn net.Conn, sandboxID string) {
	defer conn.Close()

	// v1: flat per-conn rate limiting — every accepted connection consumes one
	// token regardless of method/path. Deliberate simplification: hot path is
	// POST /boost; cheap GETs sharing the budget is acceptable for the canonical
	// 1 rps / burst 10 ceiling.
	if !h.limiter.Allow(sandboxID) {
		retry := h.limiter.RetryAfter(sandboxID)
		writeResp(conn, http.StatusTooManyRequests, map[string]string{"Retry-After": fmt.Sprintf("%d", int(retry.Seconds())+1)},
			[]byte(`{"error":"rate limit exceeded"}`))
		return
	}

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"invalid HTTP request"}`))
		return
	}

	switch {
	case req.Method == http.MethodPost && req.URL.Path == "/boost":
		h.handlePostBoost(ctx, conn, req, sandboxID)
	case req.Method == http.MethodGet && req.URL.Path == "/boost":
		h.handleGetBoost(ctx, conn, sandboxID)
	case req.Method == http.MethodDelete && req.URL.Path == "/boost":
		h.handleDeleteBoost(ctx, conn, sandboxID)
	case req.Method == http.MethodGet && req.URL.Path == "/sandbox":
		h.handleGetSandbox(ctx, conn, sandboxID)
	default:
		writeResp(conn, http.StatusNotFound, nil, []byte(`{"error":"unknown route"}`))
	}
}

func (h *BoostHTTPHandler) handlePostBoost(ctx context.Context, conn net.Conn, req *http.Request, sandboxID string) {
	var body struct {
		CPULimit        *int `json:"cpu_limit"`
		MemoryLimitMB   *int `json:"memory_limit_mb"`
		DurationSeconds int  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"invalid JSON"}`))
		return
	}
	if body.CPULimit == nil && body.MemoryLimitMB == nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"at least one of cpu_limit, memory_limit_mb is required"}`))
		return
	}
	if body.DurationSeconds <= 0 {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"duration_seconds must be > 0"}`))
		return
	}

	b, err := h.boosts.Start(ctx, service.StartBoostOpts{
		SandboxID:       sandboxID,
		CPULimit:        body.CPULimit,
		MemoryLimitMB:   body.MemoryLimitMB,
		DurationSeconds: body.DurationSeconds,
		Source:          "in_sandbox",
	})
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	respBody, _ := json.Marshal(boostToResponse(b))
	writeResp(conn, http.StatusOK, nil, respBody)
}

func (h *BoostHTTPHandler) handleGetBoost(ctx context.Context, conn net.Conn, sandboxID string) {
	b, err := h.boosts.Get(ctx, sandboxID)
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	respBody, _ := json.Marshal(boostToResponse(b))
	writeResp(conn, http.StatusOK, nil, respBody)
}

func (h *BoostHTTPHandler) handleDeleteBoost(ctx context.Context, conn net.Conn, sandboxID string) {
	if err := h.boosts.Cancel(ctx, sandboxID); err != nil {
		writeServiceError(conn, err)
		return
	}
	writeResp(conn, http.StatusNoContent, nil, nil)
}

func (h *BoostHTTPHandler) handleGetSandbox(ctx context.Context, conn net.Conn, sandboxID string) {
	sbx, err := h.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	resp := sandboxResponse{Sandbox: sbx}
	if b, err := h.boosts.Get(ctx, sandboxID); err == nil {
		br := boostToResponse(b)
		resp.ActiveBoost = &br
	}
	respBody, _ := json.Marshal(resp)
	writeResp(conn, http.StatusOK, nil, respBody)
}

// writeResp writes an HTTP/1.1 response with a JSON body and the requested
// status. extraHeaders is optional; pass nil if none.
// All header lines and the body are assembled into a single buffer and sent
// in one conn.Write so that net.Pipe callers (tests) receive everything in
// a single Read instead of multiple synchronous chunks.
func writeResp(conn net.Conn, status int, extraHeaders map[string]string, body []byte) {
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "Status"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", status, statusText)
	if body != nil {
		fmt.Fprintf(&buf, "Content-Type: application/json\r\n")
		fmt.Fprintf(&buf, "Content-Length: %s\r\n", strconv.Itoa(len(body)))
	} else {
		fmt.Fprintf(&buf, "Content-Length: 0\r\n")
	}
	for k, v := range extraHeaders {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	fmt.Fprintf(&buf, "Connection: close\r\n\r\n")
	if body != nil {
		buf.Write(body)
	}
	conn.Write(buf.Bytes()) //nolint:errcheck
}

// writeServiceError maps a service-layer error to an HTTP response.
func writeServiceError(conn net.Conn, err error) {
	status := mapErrorCode(err)
	body, _ := json.Marshal(map[string]string{"error": err.Error()})
	writeResp(conn, status, nil, body)
}
