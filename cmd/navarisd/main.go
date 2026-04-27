package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/storage"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/telemetry"
	"github.com/navaris/navaris/internal/webui"
	"github.com/navaris/navaris/internal/worker"
)

type config struct {
	listen                      string
	dbPath                      string
	logLevel                    string
	authToken                   string
	incusSocket                 string
	incusStrictPoolCoW          bool
	gcInterval                  time.Duration
	concurrency                 int
	firecrackerBin              string
	jailerBin                   string
	firecrackerDefaultVcpu      int
	firecrackerDefaultMemoryMB  int
	firecrackerVcpuHeadroomMult float64
	firecrackerMemHeadroomMult  float64
	kernelPath                  string
	imageDir                    string
	chrootBase                  string
	hostInterface               string
	snapshotDir                 string
	enableJailer                bool
	storageMode                 string
	storageRegistry             *storage.Registry
	otlpEndpoint                string
	otlpProtocol                string
	serviceName                 string
	uiPassword                  string
	uiSessionKey                string
	uiSessionTTL                time.Duration
	mcpEnabled                  bool
	mcpReadOnly                 bool
	mcpPath                     string
	mcpMaxTimeout               time.Duration
	boostMaxDuration            time.Duration
	boostChannelEnabled         bool
	boostChannelDir             string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "navarisd: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.listen, "listen", ":8080", "address to listen on")
	flag.StringVar(&cfg.dbPath, "db-path", "navaris.db", "path to SQLite database")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	flag.StringVar(&cfg.authToken, "auth-token", "", "bearer token for API authentication (empty = no auth)")
	flag.StringVar(&cfg.incusSocket, "incus-socket", "", "path to Incus socket (Firecracker > Incus > mock)")
	flag.BoolVar(&cfg.incusStrictPoolCoW, "incus-strict-pool-cow", false, "fail startup if Incus storage pool driver is not CoW-capable (default: warn)")
	flag.DurationVar(&cfg.gcInterval, "gc-interval", 5*time.Minute, "garbage collection sweep interval")
	flag.IntVar(&cfg.concurrency, "concurrency", 8, "max concurrent operations")
	flag.StringVar(&cfg.firecrackerBin, "firecracker-bin", "", "path to Firecracker binary")
	flag.StringVar(&cfg.jailerBin, "jailer-bin", "", "path to jailer binary")
	flag.StringVar(&cfg.kernelPath, "kernel-path", "", "path to vmlinux kernel")
	flag.StringVar(&cfg.imageDir, "image-dir", "", "directory containing rootfs images")
	flag.StringVar(&cfg.chrootBase, "chroot-base", "/srv/firecracker", "jailer chroot base directory")
	flag.StringVar(&cfg.hostInterface, "host-interface", "", "network interface for masquerade (auto-detect if empty)")
	flag.StringVar(&cfg.snapshotDir, "snapshot-dir", "/srv/firecracker/snapshots", "directory for Firecracker snapshots")
	flag.BoolVar(&cfg.enableJailer, "enable-jailer", true, "use the Firecracker jailer (disable for Docker-in-Docker)")
	flag.IntVar(&cfg.firecrackerDefaultVcpu, "firecracker-default-vcpu", 1, "default vCPU count for Firecracker sandboxes when CPULimit is unset")
	flag.IntVar(&cfg.firecrackerDefaultMemoryMB, "firecracker-default-memory-mb", 256, "default memory (MB, treated as MiB inside Firecracker) when MemoryLimitMB is unset")
	flag.Float64Var(&cfg.firecrackerVcpuHeadroomMult, "firecracker-vcpu-headroom-mult", 1.0, "boot-time vCPU headroom multiplier on Firecracker (>=1.0); default 1.0 (no headroom, boot at exact limit); set >1.0 to enable grow-resize within the boot-time ceiling")
	flag.Float64Var(&cfg.firecrackerMemHeadroomMult, "firecracker-mem-headroom-mult", 1.0, "boot-time memory headroom multiplier on Firecracker (>=1.0); default 1.0 (no headroom, boot at exact limit); set >1.0 to allow GROW-resize within the boot-time ceiling")
	flag.StringVar(&cfg.storageMode, "storage-mode", "auto", "CoW backend selection: auto | copy | reflink (btrfs-subvol/zfs reserved, not wired in v1)")
	flag.StringVar(&cfg.otlpEndpoint, "otlp-endpoint", "", "OTLP collector endpoint (e.g. localhost:4317); empty disables telemetry")
	flag.StringVar(&cfg.otlpProtocol, "otlp-protocol", "grpc", "OTLP transport protocol: grpc or http")
	flag.StringVar(&cfg.serviceName, "service-name", "navarisd", "service name in telemetry data")
	flag.StringVar(&cfg.uiPassword, "ui-password", "", "web UI password (empty disables the UI)")
	flag.StringVar(&cfg.uiSessionKey, "ui-session-key", "", "HMAC key for UI session cookies (empty = ephemeral per-process)")
	flag.DurationVar(&cfg.uiSessionTTL, "ui-session-ttl", 24*time.Hour, "lifetime of UI session cookies")
	flag.BoolVar(&cfg.mcpEnabled, "mcp-enabled", false, "enable the embedded /v1/mcp endpoint")
	flag.BoolVar(&cfg.mcpReadOnly, "mcp-read-only", false, "hide all mutating tools from the MCP server")
	flag.StringVar(&cfg.mcpPath, "mcp-path", "/v1/mcp", "path to mount the MCP endpoint at")
	flag.DurationVar(&cfg.mcpMaxTimeout, "mcp-max-timeout", 10*time.Minute, "cap on per-tool timeout_seconds")
	flag.DurationVar(&cfg.boostMaxDuration, "boost-max-duration", time.Hour,
		"maximum duration for a single boost (1m..24h)")
	flag.BoolVar(&cfg.boostChannelEnabled, "boost-channel-enabled", true,
		"enable the in-sandbox boost channel by default for new sandboxes")
	flag.StringVar(&cfg.boostChannelDir, "boost-channel-dir", "/var/lib/navaris/boost-channels",
		"host directory for per-sandbox Incus boost-channel UDS files")
	flag.Parse()
	return cfg
}

// buildStorageRegistry constructs the storage Registry over the
// CoW-relevant roots: chroot base, image dir, snapshot dir. It MkdirAlls
// each non-empty root before probing so a fresh install works without
// pre-creation. With mode=auto, each root is probed; explicit modes are
// hard preconditions and propagate startup-fatal errors.
func buildStorageRegistry(cfg config) (*storage.Registry, error) {
	rootSpec := []struct {
		name, path string
	}{
		{"chroot-base", cfg.chrootBase},
		{"image-dir", cfg.imageDir},
		{"snapshot-dir", cfg.snapshotDir},
	}

	var roots []string
	for _, r := range rootSpec {
		if r.path == "" {
			continue
		}
		if err := os.MkdirAll(r.path, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create %s %s: %w", r.name, r.path, err)
		}
		roots = append(roots, r.path)
	}

	return storage.BuildRegistry(
		storage.Config{Mode: storage.Mode(cfg.storageMode)},
		roots,
		nil, // per-root overrides not exposed via flags in v1
	)
}

func run(cfg config) error {
	logger := setupLogger(cfg.logLevel)
	slog.SetDefault(logger)

	telemetryShutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		Endpoint:    cfg.otlpEndpoint,
		Protocol:    cfg.otlpProtocol,
		ServiceName: cfg.serviceName,
	})
	if err != nil {
		return fmt.Errorf("telemetry init: %w", err)
	}
	if telemetry.Enabled() {
		logger.Info("telemetry enabled", "endpoint", cfg.otlpEndpoint, "protocol", cfg.otlpProtocol)
	}

	logger.Info("starting navarisd",
		"listen", cfg.listen,
		"db_path", cfg.dbPath,
		"concurrency", cfg.concurrency,
	)

	// Open store
	store, err := sqlite.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Event bus
	bus := eventbus.New(256)

	// Web UI setup — only activates when --ui-password is set.
	var (
		uiHandlers *webui.Handlers
		sessionKey []byte
	)
	if cfg.uiPassword != "" {
		if cfg.uiSessionKey != "" {
			sessionKey = []byte(cfg.uiSessionKey)
		} else {
			// 32 random bytes — used directly as the HMAC-SHA256 key.
			// Hex-encoding would be log-friendly but we never log the key,
			// so the extra step just wastes entropy on redundant encoding
			// and creates a format asymmetry with the operator-supplied path.
			sessionKey = make([]byte, 32)
			if _, err := rand.Read(sessionKey); err != nil {
				return fmt.Errorf("generate ephemeral session key: %w", err)
			}
			logger.Warn("ui-session-key not set; generated ephemeral key; sessions will not survive restart — set --ui-session-key to persist sessions")
		}
		uiHandlers = webui.NewHandlers(webui.Config{
			Password:   cfg.uiPassword,
			SessionKey: sessionKey,
			SessionTTL: cfg.uiSessionTTL,
		})
		if webui.Assets == nil {
			logger.Warn("web UI enabled but binary was built without -tags withui; /ui/* API routes are reachable but the SPA shell will not be served")
		}
		logger.Info("web UI enabled", "session_ttl", cfg.uiSessionTTL.String())
	} else {
		logger.Info("web UI disabled (--ui-password not set)")
	}

	// Worker dispatcher
	disp := worker.NewDispatcher(store.OperationStore(), bus, cfg.concurrency)

	// Storage backends: probe CoW capability per-root so the Firecracker
	// provider can use reflink where available, copy otherwise.
	storageReg, err := buildStorageRegistry(cfg)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	cfg.storageRegistry = storageReg
	{
		resolved := func(p string) string {
			if p == "" {
				return ""
			}
			return storageReg.For(p).Name()
		}
		logger.Info("storage backends",
			"mode", cfg.storageMode,
			"chroot_base", resolved(cfg.chrootBase),
			"image_dir", resolved(cfg.imageDir),
			"snapshot_dir", resolved(cfg.snapshotDir),
		)
	}

	// Provider registry — enable all configured backends.
	reg := provider.NewRegistry()
	// builtProviders collects every successfully constructed provider so that
	// SetBoostHandler can be called on them after the handler is built.
	var builtProviders []domain.Provider

	if cfg.incusSocket != "" {
		p, err := newIncusProvider(cfg)
		if err != nil {
			return fmt.Errorf("incus provider: %w", err)
		}
		reg.Register("incus", p)
		builtProviders = append(builtProviders, p)
		logger.Info("incus provider enabled", "socket", cfg.incusSocket)
	}

	if cfg.firecrackerBin != "" {
		if !kvmAvailable() {
			logger.Warn("KVM not available (/dev/kvm), firecracker provider disabled")
		} else {
			p, err := newFirecrackerProvider(cfg)
			if err != nil {
				return fmt.Errorf("firecracker provider: %w", err)
			}
			reg.Register("firecracker", p)
			builtProviders = append(builtProviders, p)
			logger.Info("firecracker provider enabled")
		}
	}

	if reg.Len() == 0 {
		// In dev (no real providers configured) register the same mock
		// instance under every known backend name. resolveBackend picks
		// "incus" or "firecracker" from the image ref alone, so without
		// these aliases any UI preset would 500 with `provider
		// "incus"/"firecracker" not available` — see
		// internal/service/sandbox.go:resolveBackend and
		// internal/provider/registry.go:resolve. This branch only runs
		// when neither --incus-socket nor --firecracker-bin is set, so
		// production behavior is unchanged.
		mock := provider.NewMock()
		reg.Register("mock", mock)
		reg.Register("incus", mock)
		reg.Register("firecracker", mock)
		logger.Info("no providers configured, using mock (aliased as incus/firecracker)")
	}

	// Set default backend: incus > firecracker > mock.
	for _, name := range []string{"incus", "firecracker", "mock"} {
		if reg.Has(name) {
			reg.SetFallback(name)
			break
		}
	}

	var prov domain.Provider = reg
	backendName := reg.Fallback()

	// Services
	if cfg.boostMaxDuration < time.Minute || cfg.boostMaxDuration > 24*time.Hour {
		slog.Error("--boost-max-duration must be in [1m, 24h]", "got", cfg.boostMaxDuration)
		os.Exit(1)
	}
	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
		store.SessionStore(), prov, bus, disp, backendName, cfg.boostChannelEnabled,
	)
	snapSvc := service.NewSnapshotService(
		store.SnapshotStore(), store.SandboxStore(), store.OperationStore(),
		prov, bus, disp,
	)
	imgSvc := service.NewImageService(
		store.ImageStore(), store.SnapshotStore(), store.OperationStore(),
		prov, bus, disp,
	)
	sessSvc := service.NewSessionService(
		store.SessionStore(), store.SandboxStore(), prov, bus,
	)
	sbxSvc.SetSessionService(sessSvc)
	boostSvc := service.NewBoostService(
		store.BoostStore(), store.SandboxStore(), sbxSvc, bus,
		service.RealClock{}, cfg.boostMaxDuration,
	)
	sbxSvc.SetBoostService(boostSvc)

	// Construct the in-sandbox boost channel handler and inject it into every
	// provider that supports it. The handler depends on boostSvc (which depends
	// on sbxSvc/prov), so providers are registered first above, and we wire the
	// handler here via a type assertion to a local interface.
	rateLim := api.NewRateLimiterDefault()
	boostHandler := api.NewBoostHTTPHandler(boostSvc, store.SandboxStore(), rateLim)
	type boostHandlerSetter interface {
		SetBoostHandler(provider.BoostServer)
	}
	for _, p := range builtProviders {
		if s, ok := p.(boostHandlerSetter); ok {
			s.SetBoostHandler(boostHandler)
		}
	}

	if err := boostSvc.Recover(context.Background()); err != nil {
		slog.Error("boost recover", "error", err)
		os.Exit(1)
	}
	opsSvc := service.NewOperationService(store.OperationStore(), disp)

	// Ensure a default project exists so the UI is usable immediately.
	if _, err := projSvc.GetByName(context.Background(), "default"); err != nil {
		if _, createErr := projSvc.Create(context.Background(), "default", nil); createErr != nil {
			logger.Warn("could not create default project", "error", createErr)
		} else {
			logger.Info("created default project")
		}
	}

	// API server
	var mcpHandler http.Handler
	if cfg.mcpEnabled {
		if cfg.authToken == "" {
			logger.Warn("MCP is enabled but --auth-token is empty; the /v1/mcp endpoint will accept unauthenticated requests including mutating tools — set --auth-token in any non-isolated environment")
		}
		// normalizeListen turns ":port" into "127.0.0.1:port" for outbound calls;
		// the loopback form is required because the MCP handler calls navarisd as a client.
		localURL := "http://" + normalizeListen(cfg.listen)
		mcpHandler = internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
			LocalAPIURL: localURL,
			AuthToken:   cfg.authToken,
			ReadOnly:    cfg.mcpReadOnly,
			MaxTimeout:  cfg.mcpMaxTimeout,
		})
		logger.Info("MCP endpoint enabled", "path", cfg.mcpPath)
	}

	srv := api.NewServer(api.ServerConfig{
		Projects:     projSvc,
		Sandboxes:    sbxSvc,
		Boosts:       boostSvc,
		Snapshots:    snapSvc,
		Images:       imgSvc,
		Sessions:     sessSvc,
		Operations:   opsSvc,
		Provider:     prov,
		Events:       bus,
		Ports:        store.PortBindingStore(),
		AuthToken:    cfg.authToken,
		Logger:       logger,
		UISessionKey: sessionKey,
		UIHandlers:   uiHandlers,
		UIAssets:     webui.Assets,
		MCPHandler:   mcpHandler,
		MCPPath:      cfg.mcpPath,
	})

	// Start dispatcher and GC
	disp.Start()

	// Reconcile stale state from previous run
	reconciler := service.NewReconciler(store.SandboxStore(), store.OperationStore(), prov, logger)
	result := reconciler.Run(context.Background())
	if len(result.Errors) > 0 {
		logger.Warn("reconciliation completed with errors", "errors", len(result.Errors))
	}
	if result.TransitionalFixed+result.StaleOpsFailed+result.DriftFixed > 0 {
		logger.Info("reconciliation results",
			"transitional_fixed", result.TransitionalFixed,
			"stale_ops_failed", result.StaleOpsFailed,
			"drift_fixed", result.DriftFixed,
		)
	}

	gc := worker.NewGC(store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), prov, worker.GCConfig{
		Interval: cfg.gcInterval,
	})
	gc.Start()

	// HTTP server
	httpSrv := &http.Server{
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("listening", "addr", ln.Addr().String())

	// Graceful shutdown on signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	// Shutdown sequence
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	gc.Stop()
	disp.Stop()

	telShutdownCtx, telCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer telCancel()
	if err := telemetryShutdown(telShutdownCtx); err != nil {
		logger.Error("telemetry shutdown error", "error", err)
	}

	logger.Info("stopped")
	return nil
}

func setupLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// normalizeListen converts a listen address to a loopback form suitable for
// the MCP handler's outbound calls back into navarisd. Wildcard hosts ("",
// "0.0.0.0", "::") become 127.0.0.1; explicit hosts are preserved. Invalid
// addresses pass through so net.Listen reports the canonical error.
func normalizeListen(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
