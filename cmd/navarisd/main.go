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
	"github.com/navaris/navaris/internal/worker"
)

type config struct {
	listen      string
	dbPath      string
	logLevel    string
	authToken   string
	incusSocket string
	gcInterval  time.Duration
	concurrency int
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
	flag.StringVar(&cfg.incusSocket, "incus-socket", "", "path to Incus socket (empty = mock provider)")
	flag.DurationVar(&cfg.gcInterval, "gc-interval", 5*time.Minute, "garbage collection sweep interval")
	flag.IntVar(&cfg.concurrency, "concurrency", 8, "max concurrent operations")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	logger := setupLogger(cfg.logLevel)
	slog.SetDefault(logger)

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

	// Provider
	var prov domain.Provider
	if cfg.incusSocket != "" {
		p, err := newIncusProvider(cfg.incusSocket)
		if err != nil {
			return fmt.Errorf("incus provider: %w", err)
		}
		prov = p
		logger.Info("using incus provider", "socket", cfg.incusSocket)
	} else {
		prov = provider.NewMock()
		logger.Info("using mock provider")
	}

	// Services
	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
		store.SessionStore(), prov, bus, disp,
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
