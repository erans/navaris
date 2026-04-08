package api

import (
	"log/slog"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/telemetry"
)

type ServerConfig struct {
	Projects   *service.ProjectService
	Sandboxes  *service.SandboxService
	Snapshots  *service.SnapshotService
	Images     *service.ImageService
	Sessions   *service.SessionService
	Operations *service.OperationService
	Provider   domain.Provider
	Events     domain.EventBus
	Ports      domain.PortBindingStore
	AuthToken    string
	UISessionKey []byte
	Logger       *slog.Logger
}

type Server struct {
	cfg ServerConfig
	log *slog.Logger
}

func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, log: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /v1/health", s.healthCheck)

	// Projects
	mux.HandleFunc("POST /v1/projects", s.createProject)
	mux.HandleFunc("GET /v1/projects", s.listProjects)
	mux.HandleFunc("GET /v1/projects/{id}", s.getProject)
	mux.HandleFunc("PUT /v1/projects/{id}", s.updateProject)
	mux.HandleFunc("DELETE /v1/projects/{id}", s.deleteProject)

	// Sandboxes
	mux.HandleFunc("POST /v1/sandboxes", s.createSandbox)
	mux.HandleFunc("POST /v1/sandboxes/from-snapshot", s.createSandboxFromSnapshot)
	mux.HandleFunc("POST /v1/sandboxes/from-image", s.createSandboxFromImage)
	mux.HandleFunc("GET /v1/sandboxes", s.listSandboxes)
	mux.HandleFunc("GET /v1/sandboxes/{id}", s.getSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{id}/start", s.startSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{id}/stop", s.stopSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{id}/destroy", s.destroySandbox)

	// Snapshots
	mux.HandleFunc("POST /v1/sandboxes/{id}/snapshots", s.createSnapshot)
	mux.HandleFunc("GET /v1/sandboxes/{id}/snapshots", s.listSnapshots)
	mux.HandleFunc("GET /v1/snapshots/{id}", s.getSnapshot)
	mux.HandleFunc("POST /v1/snapshots/{id}/restore", s.restoreSnapshot)
	mux.HandleFunc("DELETE /v1/snapshots/{id}", s.deleteSnapshot)

	// Images
	mux.HandleFunc("POST /v1/images", s.promoteImage)
	mux.HandleFunc("POST /v1/images/register", s.registerImage)
	mux.HandleFunc("GET /v1/images", s.listImages)
	mux.HandleFunc("GET /v1/images/{id}", s.getImage)
	mux.HandleFunc("DELETE /v1/images/{id}", s.deleteImage)

	// Sessions
	mux.HandleFunc("POST /v1/sandboxes/{id}/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sandboxes/{id}/sessions", s.listSessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.deleteSession)

	// Operations
	mux.HandleFunc("GET /v1/operations/{id}", s.getOperation)
	mux.HandleFunc("GET /v1/operations", s.listOperations)
	mux.HandleFunc("POST /v1/operations/{id}/cancel", s.cancelOperation)

	// Ports
	mux.HandleFunc("POST /v1/sandboxes/{id}/ports", s.createPort)
	mux.HandleFunc("GET /v1/sandboxes/{id}/ports", s.listPorts)
	mux.HandleFunc("DELETE /v1/sandboxes/{id}/ports/{target_port}", s.deletePort)

	// Exec
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", s.execInSandbox)

	// Events (WebSocket)
	mux.HandleFunc("GET /v1/events", s.streamEvents)

	// Apply middleware chain: requestID -> auth -> logging -> mux
	var handler http.Handler = mux
	handler = loggingMiddleware(s.log)(handler)
	handler = authMiddleware(s.cfg.AuthToken, s.cfg.UISessionKey)(handler)
	handler = requestIDMiddleware(handler)

	// Telemetry middleware (outermost when enabled):
	// tracing -> metrics -> requestID -> auth -> logging -> mux
	if telemetry.Enabled() {
		mw, err := newMetricsMiddleware()
		if err != nil {
			s.log.Error("failed to create metrics middleware", "error", err)
		} else {
			handler = mw(handler)
		}
		handler = newTracingMiddleware()(handler)
	}

	return handler
}
