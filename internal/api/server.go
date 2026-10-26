package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"agent-dashboard/internal/core/auth"
	"agent-dashboard/internal/store"
)

type Server struct {
	cfg        Config
	store      *store.Store
	logger     *slog.Logger
	httpServer *http.Server
	mux        *http.ServeMux
	webFS      fs.FS
}

func NewServer(cfg Config, s *store.Store, logger *slog.Logger, webFS fs.FS) (*Server, error) {
	mux := http.NewServeMux()
	limiter := NewRateLimiterMiddleware(auth.DefaultRateLimiterConfig())

	authHandler, err := NewAuthHandler(s, cfg, limiter)
	if err != nil {
		return nil, err
	}

	telemetryHandler := NewTelemetryHandler(cfg.DataDir)
	secretsHandler := NewSecretsHandler(s, cfg.MasterPassphrase)
	auditHandler := NewAuditHandler(s)

	// Health check (unauthenticated)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Public Auth endpoints (rate limited)
	mux.HandleFunc("GET /api/auth/setup/status", authHandler.HandleSetupStatus)
	mux.HandleFunc("POST /api/auth/setup", limiter.Limit(authHandler.HandleSetup))
	mux.HandleFunc("POST /api/auth/login", limiter.Limit(authHandler.HandleLogin))
	mux.HandleFunc("POST /api/auth/passkey/begin-login", limiter.Limit(authHandler.HandlePasskeyBeginLogin))
	mux.HandleFunc("POST /api/auth/passkey/finish-login", limiter.Limit(authHandler.HandlePasskeyFinishLogin))

	// Protected Auth endpoints
	mux.HandleFunc("POST /api/auth/logout", RequireAuth(s, authHandler.HandleLogout))
	mux.HandleFunc("GET /api/auth/me", RequireAuth(s, authHandler.HandleMe))
	mux.HandleFunc("POST /api/auth/passkey/begin-registration", RequireAuth(s, authHandler.HandlePasskeyBeginRegistration))
	mux.HandleFunc("POST /api/auth/passkey/finish-registration", RequireAuth(s, authHandler.HandlePasskeyFinishRegistration))

	// Protected Telemetry & System
	mux.HandleFunc("GET /api/system", RequireAuth(s, telemetryHandler.HandleGetSystem))

	// Protected Secrets
	mux.HandleFunc("GET /api/secrets", RequireAuth(s, secretsHandler.HandleList))
	mux.HandleFunc("POST /api/secrets", RequireAuth(s, secretsHandler.HandleCreate))
	mux.HandleFunc("DELETE /api/secrets/{name}", RequireAuth(s, secretsHandler.HandleDelete))

	// Protected Audit Log
	mux.HandleFunc("GET /api/audit-log", RequireAuth(s, auditHandler.HandleGetLogs))

	// SPA & Static file handling
	if webFS != nil {
		fileServer := http.FileServer(http.FS(webFS))
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			// Don't intercept API paths
			if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
				http.NotFound(w, r)
				return
			}

			// Try to open static file
			cleanPath := strings.TrimPrefix(r.URL.Path, "/")
			if cleanPath == "" {
				cleanPath = "index.html"
			}

			f, err := webFS.Open(cleanPath)
			if err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}

			// SPA fallback: serve index.html for client-side routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		})
	}

	// Middleware chain: Recovery -> SecurityHeaders -> CSRFProtection -> Handler
	var handler http.Handler = mux
	handler = CSRFProtectionMiddleware(handler)
	handler = SecurityHeadersMiddleware(handler)
	handler = RecoveryMiddleware(logger, handler)

	httpServer := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &Server{
		cfg:        cfg,
		store:      s,
		logger:     logger,
		httpServer: httpServer,
		mux:        mux,
		webFS:      webFS,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.BindAddr)
	if err != nil {
		return err
	}
	s.logger.Info("server listening", "bind_addr", s.cfg.BindAddr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}
