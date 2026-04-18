package mcp

import (
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// Options configures the MCP server.
type Options struct {
	// Client is the navaris HTTP client used by all tool handlers.
	Client *client.Client

	// ReadOnly hides every mutating tool when true.
	ReadOnly bool

	// MaxTimeout caps any per-tool timeout_seconds argument. Defaults to 600s.
	MaxTimeout time.Duration

	// Version is the server version string (defaults to "dev").
	Version string
}

func (o Options) maxTimeout() time.Duration {
	if o.MaxTimeout > 0 {
		return o.MaxTimeout
	}
	return 600 * time.Second
}

func (o Options) version() string {
	if o.Version != "" {
		return o.Version
	}
	return "dev"
}
