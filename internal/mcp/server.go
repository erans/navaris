package mcp

import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer constructs an *mcp.Server with the navaris tool set registered.
// The set of tools depends on opts.ReadOnly.
func NewServer(opts Options) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "navaris",
		Version: opts.version(),
	}, nil)
	register(s, opts)
	return s
}
