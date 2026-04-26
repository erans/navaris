package api

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/telemetry"
	"github.com/navaris/navaris/internal/webui"
)

type ServerConfig struct {
	Projects     *service.ProjectService
	Sandboxes    *service.SandboxService
	Snapshots    *service.SnapshotService
	Images       *service.ImageService
	Sessions     *service.SessionService
	Operations   *service.OperationService
	Provider     domain.Provider
	Events       domain.EventBus
	Ports        domain.PortBindingStore
	AuthToken    string
	Logger       *slog.Logger
	UISessionKey []byte
	UIHandlers   *webui.Handlers // nil means UI disabled
	UIAssets     fs.FS           // nil means UI disabled
	MCPHandler   http.Handler    // optional; when non-nil, mounted at MCPPath inside the auth-protected /v1/ sub-mux
	MCPPath      string          // defaults to "/v1/mcp" when MCPHandler is set
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
	// Inner mux: all /v1/* routes (protected by authMiddleware).
	api := http.NewServeMux()

	api.HandleFunc("GET /v1/health", s.healthCheck)

	api.HandleFunc("POST /v1/projects", s.createProject)
	api.HandleFunc("GET /v1/projects", s.listProjects)
	api.HandleFunc("GET /v1/projects/{id}", s.getProject)
	api.HandleFunc("PUT /v1/projects/{id}", s.updateProject)
	api.HandleFunc("DELETE /v1/projects/{id}", s.deleteProject)

	api.HandleFunc("POST /v1/sandboxes", s.createSandbox)
	api.HandleFunc("POST /v1/sandboxes/from-snapshot", s.createSandboxFromSnapshot)
	api.HandleFunc("POST /v1/sandboxes/from-image", s.createSandboxFromImage)
	api.HandleFunc("GET /v1/sandboxes", s.listSandboxes)
	api.HandleFunc("GET /v1/sandboxes/{id}", s.getSandbox)
	api.HandleFunc("POST /v1/sandboxes/{id}/start", s.startSandbox)
	api.HandleFunc("POST /v1/sandboxes/{id}/stop", s.stopSandbox)
	api.HandleFunc("POST /v1/sandboxes/{id}/destroy", s.destroySandbox)
	api.HandleFunc("POST /v1/sandboxes/{id}/fork", s.forkSandbox)
	api.HandleFunc("PATCH /v1/sandboxes/{id}/resources", s.updateSandboxResources)

	api.HandleFunc("POST /v1/sandboxes/{id}/snapshots", s.createSnapshot)
	api.HandleFunc("GET /v1/sandboxes/{id}/snapshots", s.listSnapshots)
	api.HandleFunc("GET /v1/snapshots/{id}", s.getSnapshot)
	api.HandleFunc("POST /v1/snapshots/{id}/restore", s.restoreSnapshot)
	api.HandleFunc("DELETE /v1/snapshots/{id}", s.deleteSnapshot)

	api.HandleFunc("POST /v1/images", s.promoteImage)
	api.HandleFunc("POST /v1/images/register", s.registerImage)
	api.HandleFunc("GET /v1/images", s.listImages)
	api.HandleFunc("GET /v1/images/{id}", s.getImage)
	api.HandleFunc("DELETE /v1/images/{id}", s.deleteImage)

	api.HandleFunc("POST /v1/sandboxes/{id}/sessions", s.createSession)
	api.HandleFunc("GET /v1/sandboxes/{id}/sessions", s.listSessions)
	api.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	api.HandleFunc("DELETE /v1/sessions/{id}", s.deleteSession)

	api.HandleFunc("GET /v1/operations/{id}", s.getOperation)
	api.HandleFunc("GET /v1/operations", s.listOperations)
	api.HandleFunc("POST /v1/operations/{id}/cancel", s.cancelOperation)

	api.HandleFunc("POST /v1/sandboxes/{id}/ports", s.createPort)
	api.HandleFunc("GET /v1/sandboxes/{id}/ports", s.listPorts)
	api.HandleFunc("DELETE /v1/sandboxes/{id}/ports/{target_port}", s.deletePort)

	api.HandleFunc("POST /v1/sandboxes/{id}/exec", s.execInSandbox)
	api.HandleFunc("GET /v1/events", s.streamEvents)
	api.HandleFunc("GET /v1/sandboxes/{id}/attach", s.attachSandbox)

	// Mount the embedded MCP handler when enabled. The handler is registered
	// on the bare path (no method prefix) so the SDK can serve both the
	// initialising POST and subsequent SSE GETs on the same path.
	if s.cfg.MCPHandler != nil {
		mcpPath := s.cfg.MCPPath
		if mcpPath == "" {
			mcpPath = "/v1/mcp"
		}
		api.Handle(mcpPath, s.cfg.MCPHandler)
	}

	// Wrap the /v1 sub-mux with logging + auth.
	var apiHandler http.Handler = api
	apiHandler = loggingMiddleware(s.log)(apiHandler)
	apiHandler = authMiddleware(s.cfg.AuthToken, s.cfg.UISessionKey)(apiHandler)

	// Root mux: dispatches /v1/* to the protected sub-mux and /ui/* + / to
	// the UI handlers when enabled. Note that only /v1/* requests flow
	// through loggingMiddleware — /ui/* and / share requestID and telemetry
	// wrapping but are not emitted on the structured access log. When
	// operational visibility for UI auth events becomes important, move
	// loggingMiddleware out to wrap `root` instead.
	root := http.NewServeMux()
	root.Handle("/v1/", apiHandler)

	if s.cfg.UIHandlers != nil {
		root.HandleFunc("POST /ui/login", s.cfg.UIHandlers.Login)
		root.HandleFunc("POST /ui/logout", s.cfg.UIHandlers.Logout)
		root.HandleFunc("GET /ui/me", s.cfg.UIHandlers.Me)
		// Catch-all under /ui/ — prevents unknown variants from falling
		// through to the SPA asset handler.
		root.Handle("/ui/", http.HandlerFunc(s.cfg.UIHandlers.NotAllowed))
	}
	if s.cfg.UIAssets != nil {
		root.Handle("/", webui.NewAssetHandler(s.cfg.UIAssets))
	}

	// Outer middleware — requestID wraps everything. When telemetry is
	// enabled, the full chain for /v1/* requests is:
	//   tracing → metrics → requestID → root → auth → logging → api mux
	// and for /ui/* and / requests it is:
	//   tracing → metrics → requestID → root → (UI handler or asset)
	var handler http.Handler = root
	handler = requestIDMiddleware(handler)

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
