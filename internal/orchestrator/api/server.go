// Package api provides the HTTP API server for the orchestrator.
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"cymbytes.com/cymconductor/internal/orchestrator/api/handlers"
	"cymbytes.com/cymconductor/internal/orchestrator/registry"
	"cymbytes.com/cymconductor/internal/orchestrator/scheduler"
	"cymbytes.com/cymconductor/internal/orchestrator/storage"
)

// Server is the HTTP API server.
type Server struct {
	router   chi.Router
	server   *http.Server
	logger   zerolog.Logger
	handlers *handlers.Handlers
}

// Config holds server configuration.
type Config struct {
	Host         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Downloads directory for agent binaries
	DownloadsDir string

	// Web directory for dashboard static files
	WebDir string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Host:         "0.0.0.0",
		Port:         8081,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		DownloadsDir: "/srv/downloads",
	}
}

// Dependencies holds the dependencies needed by the API handlers.
type Dependencies struct {
	DB        *storage.DB
	Registry  *registry.Registry
	Scheduler *scheduler.Scheduler
	Version   string
	StartTime time.Time
}

// New creates a new API server.
func New(cfg Config, deps Dependencies, logger zerolog.Logger) *Server {
	logger = logger.With().Str("component", "api").Logger()

	// Create handlers
	h := handlers.New(deps.DB, deps.Registry, deps.Scheduler, deps.Version, deps.StartTime, logger)

	// Create router
	router := chi.NewRouter()

	// Middleware stack
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(corsMiddleware)
	router.Use(requestLogger(logger))
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(cfg.ReadTimeout))

	// Routes
	router.Route("/api", func(r chi.Router) {
		// Agent endpoints
		r.Route("/agents", func(r chi.Router) {
			r.Post("/register", h.RegisterAgent)
			r.Get("/", h.ListAgents)

			r.Route("/{agentID}", func(r chi.Router) {
				r.Post("/heartbeat", h.AgentHeartbeat)
				r.Get("/", h.GetAgent)

				// Job endpoints for agents
				r.Route("/jobs", func(r chi.Router) {
					r.Get("/next", h.GetNextJobs)
					r.Post("/{jobID}/result", h.SubmitJobResult)
				})
			})
		})

		// Scenario endpoints
		r.Route("/scenarios", func(r chi.Router) {
			r.Post("/", h.CreateScenario)
			r.Get("/", h.ListScenarios)

			r.Route("/{scenarioID}", func(r chi.Router) {
				r.Get("/", h.GetScenario)
				r.Get("/status", h.GetScenarioStatus)
				r.Delete("/", h.DeleteScenario)
			})
		})

		// Job admin endpoints
		r.Route("/jobs", func(r chi.Router) {
			r.Get("/stats", h.GetJobStats)
		})

		// Debug endpoints (for development/testing)
		r.Route("/debug", func(r chi.Router) {
			r.Post("/test-job", h.CreateTestJob)
		})

		// User management endpoints (impersonation users)
		r.Route("/users", func(r chi.Router) {
			r.Post("/", h.CreateImpersonationUser)
			r.Post("/bulk", h.BulkCreateImpersonationUsers)
			r.Get("/", h.ListImpersonationUsers)

			r.Route("/{userID}", func(r chi.Router) {
				r.Get("/", h.GetImpersonationUser)
				r.Put("/", h.UpdateImpersonationUser)
				r.Delete("/", h.DeleteImpersonationUser)
			})
		})
	})

	// Health and utility endpoints
	router.Get("/health", h.HealthCheck)
	router.Get("/ready", h.ReadyCheck)

	// Agent binary downloads
	if cfg.DownloadsDir != "" {
		fileServer := http.FileServer(http.Dir(cfg.DownloadsDir))
		router.Handle("/downloads/*", http.StripPrefix("/downloads/", fileServer))
	}

	// Web dashboard (serve static files with index.html fallback)
	if cfg.WebDir != "" {
		router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			// Serve index.html for root path
			path := r.URL.Path
			if path == "/" {
				http.ServeFile(w, r, cfg.WebDir+"/index.html")
				return
			}
			// Serve static files
			http.ServeFile(w, r, cfg.WebDir+path)
		})
	}

	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return &Server{
		router:   router,
		server:   server,
		logger:   logger,
		handlers: h,
	}
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	s.logger.Info().Str("addr", s.server.Addr).Msg("Starting HTTP server")
	return s.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info().Msg("Shutting down HTTP server")
	return s.server.Shutdown(ctx)
}

// Router returns the chi router for testing.
func (s *Server) Router() chi.Router {
	return s.router
}

// requestLogger returns a middleware that logs requests.
func requestLogger(logger zerolog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				status := ww.Status()
				duration := time.Since(start)

				// Log at appropriate level
				event := logger.Info()
				if status >= 500 {
					event = logger.Error()
				} else if status >= 400 {
					event = logger.Warn()
				}

				event.
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Int("status", status).
					Dur("duration", duration).
					Str("remote", r.RemoteAddr).
					Str("request_id", middleware.GetReqID(r.Context())).
					Msg("Request completed")
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// corsMiddleware adds CORS headers for development and cross-origin requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
