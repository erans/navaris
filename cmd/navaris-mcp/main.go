package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/pkg/client"
)

func main() {
	configureLogging()

	apiURL := os.Getenv("NAVARIS_API_URL")
	if apiURL == "" {
		fatal(fmt.Errorf("NAVARIS_API_URL is required"))
	}

	token := os.Getenv("NAVARIS_TOKEN")
	readOnly := envBool("NAVARIS_MCP_READ_ONLY")
	maxTimeout := envDuration("NAVARIS_MCP_MAX_TIMEOUT", 600*time.Second)

	c := client.NewClient(client.WithURL(apiURL), client.WithToken(token))
	srv := internalmcp.NewServer(internalmcp.Options{
		Client:     c,
		ReadOnly:   readOnly,
		MaxTimeout: maxTimeout,
		Version:    versionString(),
	})

	if err := srv.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		fatal(err)
	}
}

// envBool returns true when the named env var is a valid true-ish value.
func envBool(name string) bool {
	v, err := strconv.ParseBool(os.Getenv(name))
	return err == nil && v
}

// envDuration parses a Go duration from the named env var, falling back to
// fallback when unset or invalid.
func envDuration(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid duration env var, using default", "name", name, "value", raw, "default", fallback)
		return fallback
	}
	return d
}

// configureLogging sets up structured logging to stderr. Stdout is reserved
// for the MCP protocol framing.
func configureLogging() {
	format := os.Getenv("NAVARIS_MCP_LOG_FORMAT")
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))
}

// versionString returns the version from the environment or "dev".
func versionString() string {
	if v := os.Getenv("NAVARIS_MCP_VERSION"); v != "" {
		return v
	}
	return "dev"
}

func fatal(err error) {
	slog.Error("navaris-mcp exiting", "error", err)
	os.Exit(1)
}
