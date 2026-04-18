package mcp

import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// register adds tools to the given server based on the options.
// Each per-category function is a no-op stub here; M7 and M8 fill them in.
func register(s *mcpsdk.Server, opts Options) {
	registerProjectTools(s, opts)
	registerSandboxReadTools(s, opts)
	registerSessionReadTools(s, opts)
	registerSnapshotReadTools(s, opts)
	registerImageTools(s, opts)
	registerOperationReadTools(s, opts)

	if opts.ReadOnly {
		return
	}

	registerSandboxMutatingTools(s, opts)
	registerSessionMutatingTools(s, opts)
	registerSnapshotMutatingTools(s, opts)
	registerOperationMutatingTools(s, opts)
}

// Stubs — each milestone fills these in as resources are added.
func registerSandboxMutatingTools(s *mcpsdk.Server, opts Options)   {}
func registerSessionMutatingTools(s *mcpsdk.Server, opts Options)   {}
func registerSnapshotMutatingTools(s *mcpsdk.Server, opts Options)  {}
func registerImageTools(s *mcpsdk.Server, opts Options)             {}
func registerOperationReadTools(s *mcpsdk.Server, opts Options)     {}
func registerOperationMutatingTools(s *mcpsdk.Server, opts Options) {}
