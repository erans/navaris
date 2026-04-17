package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"nhooyr.io/websocket"
)

// AttachConn is a thin wrapper around a WebSocket connected to a sandbox's
// attach endpoint. Read returns output bytes from the sandbox; Write sends
// input bytes; Resize sends a JSON resize control frame; Close terminates.
type AttachConn struct {
	ws       *websocket.Conn
	ctx      context.Context
	leftover []byte // bytes from a frame that overflowed the caller's buffer
}

// Read returns output bytes from the sandbox. Returns io.EOF when the
// WebSocket closes cleanly. If a frame's payload exceeds the caller's
// buffer, the tail is stashed and returned on subsequent Read calls.
func (a *AttachConn) Read(p []byte) (int, error) {
	if len(a.leftover) > 0 {
		n := copy(p, a.leftover)
		a.leftover = a.leftover[n:]
		return n, nil
	}
	_, data, err := a.ws.Read(a.ctx)
	if err != nil {
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return 0, io.EOF
		}
		return 0, err
	}
	n := copy(p, data)
	if n < len(data) {
		a.leftover = append(a.leftover[:0], data[n:]...)
	}
	return n, nil
}

// Write sends input bytes to the sandbox as a binary frame.
func (a *AttachConn) Write(p []byte) (int, error) {
	if err := a.ws.Write(a.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sends a JSON resize control frame: {"type":"resize","cols":C,"rows":R}.
func (a *AttachConn) Resize(cols, rows int) error {
	msg, _ := json.Marshal(map[string]any{
		"type": "resize",
		"cols": cols,
		"rows": rows,
	})
	return a.ws.Write(a.ctx, websocket.MessageText, msg)
}

// Close terminates the attach with a normal-closure status.
func (a *AttachConn) Close() error {
	return a.ws.Close(websocket.StatusNormalClosure, "client closed")
}

// AttachSandbox opens a WebSocket to /v1/sandboxes/{id}/attach. If sessionID
// is non-empty it is passed as the ?session query parameter. Auth uses the
// client's bearer token via the Authorization header.
func (c *Client) AttachSandbox(ctx context.Context, sandboxID, sessionID string) (*AttachConn, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/v1/sandboxes/%s/attach", sandboxID)
	q := u.Query()
	if sessionID != "" {
		q.Set("session", sessionID)
	}
	u.RawQuery = q.Encode()

	hdr := http.Header{}
	if c.token != "" {
		hdr.Set("Authorization", "Bearer "+c.token)
	}
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, fmt.Errorf("dial attach: %w", err)
	}
	return &AttachConn{ws: conn, ctx: ctx}, nil
}
