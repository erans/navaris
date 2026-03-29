package main

import (
	"os"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

// resolveString returns the first non-empty value from the flag, the
// environment variable, or the fallback.
func resolveString(cmd *cobra.Command, flag, envVar, fallback string) string {
	if v, _ := cmd.Flags().GetString(flag); v != "" {
		return v
	}
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// newClient builds an SDK client from resolved flags and env vars.
func newClient(cmd *cobra.Command) *client.Client {
	var opts []client.Option

	if url := resolveString(cmd, "api-url", "NAVARIS_API_URL", ""); url != "" {
		opts = append(opts, client.WithURL(url))
	}
	if tok := resolveString(cmd, "token", "NAVARIS_TOKEN", ""); tok != "" {
		opts = append(opts, client.WithToken(tok))
	}

	return client.NewClient(opts...)
}

// resolveProject returns the project ID from the --project flag or
// NAVARIS_PROJECT env var. Returns an empty string if neither is set.
func resolveProject(cmd *cobra.Command) string {
	return resolveString(cmd, "project", "NAVARIS_PROJECT", "")
}
