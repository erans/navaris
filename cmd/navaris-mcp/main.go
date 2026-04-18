package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/pkg/client"
)

// version is set at build time via:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
//
// NAVARIS_MCP_VERSION may override at runtime (used by tests).
var version = "dev"

func main() {
	configureLogging()

	apiURL := os.Getenv("NAVARIS_API_URL")
	if apiURL == "" {
		fatal(errors.New("NAVARIS_API_URL is required"))
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

	// Honor SIGINT/SIGTERM so operators can Ctrl-C cleanly. The dominant
	// termination path remains stdin EOF when the MCP client disconnects.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		fatal(err)
	}
}

// envBool returns true when the named env var parses as a true boolean
// per strconv.ParseBool. Unset is treated as false; an invalid value is
// treated as false but logged because READ_ONLY is a safety toggle and
// silent fallback would mask typos.
func envBool(name string) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn("invalid bool env var, defaulting to false", "name", name, "value", raw)
		return false
	}
	return v
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
// for the MCP protocol framing — any stray write there breaks the transport.
func configureLogging() {
	opts := &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("NAVARIS_MCP_LOG_LEVEL"))}
	var h slog.Handler
	if os.Getenv("NAVARIS_MCP_LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func versionString() string {
	if v := os.Getenv("NAVARIS_MCP_VERSION"); v != "" {
		return v
	}
	return version
}

func fatal(err error) {
	slog.Error("navaris-mcp exiting", "error", err)
	os.Exit(1)
}
