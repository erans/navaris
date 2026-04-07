package main

import (
	"context"
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
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/telemetry"
	"github.com/navaris/navaris/internal/worker"
)

type config struct {
	listen         string
	dbPath         string
	logLevel       string
	authToken      string
	incusSocket    string
	gcInterval     time.Duration
	concurrency    int
	firecrackerBin string
	jailerBin      string
	kernelPath     string
	imageDir       string
	chrootBase     string
	hostInterface  string
	snapshotDir    string
	enableJailer   bool
	otlpEndpoint   string
	otlpProtocol   string
	serviceName    string
	uiPassword     string
	uiSessionKey   string
	uiSessionTTL   time.Duration
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
	flag.StringVar(&cfg.otlpEndpoint, "otlp-endpoint", "", "OTLP collector endpoint (e.g. localhost:4317); empty disables telemetry")
	flag.StringVar(&cfg.otlpProtocol, "otlp-protocol", "grpc", "OTLP transport protocol: grpc or http")
	flag.StringVar(&cfg.serviceName, "service-name", "navarisd", "service name in telemetry data")
	flag.StringVar(&cfg.uiPassword, "ui-password", "", "web UI password (empty disables the UI)")
	flag.StringVar(&cfg.uiSessionKey, "ui-session-key", "", "HMAC key for UI session cookies (empty = ephemeral per-process)")
	flag.DurationVar(&cfg.uiSessionTTL, "ui-session-ttl", 24*time.Hour, "lifetime of UI session cookies")
	flag.Parse()
	return cfg
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

	// Worker dispatcher
	disp := worker.NewDispatcher(store.OperationStore(), bus, cfg.concurrency)

	// Provider registry — enable all configured backends.
	reg := provider.NewRegistry()

	if cfg.incusSocket != "" {
		p, err := newIncusProvider(cfg.incusSocket)
		if err != nil {
			return fmt.Errorf("incus provider: %w", err)
		}
		reg.Register("incus", p)
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
			logger.Info("firecracker provider enabled")
		}
	}

	if reg.Len() == 0 {
		reg.Register("mock", provider.NewMock())
		logger.Info("no providers configured, using mock")
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
	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
		store.SessionStore(), prov, bus, disp, backendName,
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
	opsSvc := service.NewOperationService(store.OperationStore(), disp)

	// API server
	srv := api.NewServer(api.ServerConfig{
		Projects:   projSvc,
		Sandboxes:  sbxSvc,
		Snapshots:  snapSvc,
		Images:     imgSvc,
		Sessions:   sessSvc,
		Operations: opsSvc,
		Provider:   prov,
		Events:     bus,
		Ports:      store.PortBindingStore(),
		AuthToken:  cfg.authToken,
		Logger:     logger,
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
