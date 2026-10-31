package api

import (
	"agent-dashboard/internal/shell/crypto"
	shelllogger "agent-dashboard/internal/shell/logger"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agent-dashboard/internal/core/auth"
	"agent-dashboard/internal/runtime/docker"
	"agent-dashboard/internal/runtime/host"
	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/shell/notifier"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type Server struct {
	cfg        Config
	store      *store.Store
	orch       *orchestrator.Orchestrator
	logger     *slog.Logger
	httpServer *http.Server
	mux        *http.ServeMux
	webFS      fs.FS
	ws         *WSHandler
}

func NewServer(cfg Config, s *store.Store, logger *slog.Logger, webFS fs.FS) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if _, ok := logger.Handler().(*shelllogger.ScrubbingHandler); !ok {
		logger = slog.New(shelllogger.NewScrubbingHandler(logger.Handler(), cfg.MasterPassphrase))
	}
	s.SetAuditRedactor(func(value string) string { return shelllogger.Scrub(logger, value) })
	mux := http.NewServeMux()
	limiter := NewRateLimiterMiddleware(auth.DefaultRateLimiterConfig())

	authHandler, err := NewAuthHandler(s, cfg, limiter)
	if err != nil {
		return nil, err
	}

	vault, err := crypto.NewVault(s, cfg.MasterPassphrase, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize secret vault: %w", err)
	}
	secretsHandler := &SecretsHandler{store: s, vault: vault}
	auditHandler := NewAuditHandler(s)

	// Shell & Orchestration wiring
	workspaceDir := filepath.Join(cfg.DataDir, "workspace")
	gitSvc := git.NewService(workspaceDir)
	hostRt := host.NewHostRuntimeWithLogDir(filepath.Join(cfg.DataDir, "runtime"))
	dockerRt := docker.NewDockerRuntime()
	orch := orchestrator.NewOrchestrator(s, gitSvc, dockerRt, secretsHandler, logger, 1)
	orch.RegisterRuntime("docker", dockerRt)
	orch.RegisterRuntime("host", hostRt)
	orch.SetHookURL(strings.TrimRight(cfg.RPOrigin, "/") + "/api/internal/hooks/approval")

	telemetryHandler := NewTelemetryHandler(cfg.DataDir, orch)

	// Push Notifier wiring
	pushNotifier, err := notifier.NewNotifier(s, logger, vault)
	if err != nil {
		logger.Warn("Failed to initialize push notifier", "error", err)
	} else {
		orch.SetNotifier(pushNotifier)
	}

	reposHandler := NewReposHandler(s, gitSvc, secretsHandler)
	agentsHandler := NewAgentsHandler(s, orch)
	wsHandler := NewWSHandler(s, orch, cfg.RPOrigin)
	approvalsHandler := NewApprovalsHandler(s, orch)
	pushHandler := NewPushHandler(s, pushNotifier)
	gitHandler := NewGitHandler(s, gitSvc, orch, secretsHandler)
	automationHandler := NewAutomationHandler(s, orch)
	subagentsHandler := NewSubagentsHandler(s)

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

	// Protected Repositories
	mux.HandleFunc("GET /api/repos", RequireAuth(s, reposHandler.HandleList))
	mux.HandleFunc("POST /api/repos", RequireAuth(s, reposHandler.HandleAdd))
	mux.HandleFunc("GET /api/repos/{id}", RequireAuth(s, reposHandler.HandleGet))
	mux.HandleFunc("GET /api/repos/{id}/settings", RequireAuth(s, reposHandler.HandleGetSettings))
	mux.HandleFunc("PUT /api/repos/{id}/settings", RequireAuth(s, reposHandler.HandleUpdateSettings))
	mux.HandleFunc("POST /api/repos/{id}/fetch", RequireAuth(s, reposHandler.HandleFetch))
	mux.HandleFunc("POST /api/repos/{id}/pull", RequireAuth(s, reposHandler.HandlePull))
	mux.HandleFunc("DELETE /api/repos/{id}", RequireAuth(s, reposHandler.HandleDelete))

	// Protected Agents
	mux.HandleFunc("GET /api/agents", RequireAuth(s, agentsHandler.HandleList))
	mux.HandleFunc("POST /api/agents", RequireAuth(s, agentsHandler.HandleCreate))
	mux.HandleFunc("GET /api/agents/{id}", RequireAuth(s, agentsHandler.HandleGet))
	mux.HandleFunc("POST /api/agents/{id}/message", RequireAuth(s, agentsHandler.HandleSendMessage))
	mux.HandleFunc("POST /api/agents/{id}/resume", RequireAuth(s, agentsHandler.HandleResume))
	mux.HandleFunc("POST /api/agents/{id}/interrupt", RequireAuth(s, agentsHandler.HandleInterrupt))
	mux.HandleFunc("POST /api/agents/{id}/stop", RequireAuth(s, agentsHandler.HandleStop))
	mux.HandleFunc("POST /api/agents/{id}/cancel", RequireAuth(s, agentsHandler.HandleCancel))
	mux.HandleFunc("DELETE /api/agents/{id}", RequireAuth(s, agentsHandler.HandleDelete))
	mux.HandleFunc("GET /api/agents/{id}/events", RequireAuth(s, agentsHandler.HandleGetEvents))

	// Protected Approvals
	mux.HandleFunc("GET /api/agents/{id}/approvals", RequireAuth(s, approvalsHandler.HandleList))
	mux.HandleFunc("POST /api/agents/{id}/approvals/{aid}", RequireAuth(s, approvalsHandler.HandleDecide))

	// Internal PreToolUse Hook Approval Endpoint (Local IPC)
	mux.HandleFunc("POST /api/internal/hooks/approval", approvalsHandler.HandleHookApproval)

	// Protected Web Push
	mux.HandleFunc("GET /api/push/vapid-key", RequireAuth(s, pushHandler.HandleGetVAPIDKey))
	mux.HandleFunc("POST /api/push/subscribe", RequireAuth(s, pushHandler.HandleSubscribe))
	mux.HandleFunc("POST /api/push/unsubscribe", RequireAuth(s, pushHandler.HandleUnsubscribe))
	mux.HandleFunc("POST /api/push/test", RequireAuth(s, pushHandler.HandleTestPush))

	// Protected Git Operations
	mux.HandleFunc("GET /api/agents/{id}/git/status", RequireAuth(s, gitHandler.HandleStatus))
	mux.HandleFunc("GET /api/agents/{id}/git/changes", RequireAuth(s, gitHandler.HandleChanges))
	mux.HandleFunc("GET /api/agents/{id}/git/diff", RequireAuth(s, gitHandler.HandleDiff))
	mux.HandleFunc("POST /api/agents/{id}/git/stage", RequireAuth(s, gitHandler.HandleStage))
	mux.HandleFunc("POST /api/agents/{id}/git/unstage", RequireAuth(s, gitHandler.HandleUnstage))
	mux.HandleFunc("POST /api/agents/{id}/git/stage-hunk", RequireAuth(s, gitHandler.HandleStageHunk))
	mux.HandleFunc("POST /api/agents/{id}/git/commit", RequireAuth(s, gitHandler.HandleCommit))
	mux.HandleFunc("POST /api/agents/{id}/git/branch", RequireAuth(s, gitHandler.HandleBranch))
	mux.HandleFunc("POST /api/agents/{id}/git/push", RequireAuth(s, gitHandler.HandlePush))
	mux.HandleFunc("POST /api/agents/{id}/git/pull-rebase", RequireAuth(s, gitHandler.HandlePullRebase))
	mux.HandleFunc("POST /api/agents/{id}/git/pr", RequireAuth(s, gitHandler.HandleCreatePR))

	// Protected Templates
	mux.HandleFunc("GET /api/templates", RequireAuth(s, automationHandler.HandleListTemplates))
	mux.HandleFunc("POST /api/templates", RequireAuth(s, automationHandler.HandleCreateTemplate))
	mux.HandleFunc("GET /api/templates/{id}", RequireAuth(s, automationHandler.HandleGetTemplate))
	mux.HandleFunc("PUT /api/templates/{id}", RequireAuth(s, automationHandler.HandleUpdateTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", RequireAuth(s, automationHandler.HandleDeleteTemplate))

	// Protected Schedules
	mux.HandleFunc("GET /api/schedules", RequireAuth(s, automationHandler.HandleListSchedules))
	mux.HandleFunc("POST /api/schedules", RequireAuth(s, automationHandler.HandleCreateSchedule))
	mux.HandleFunc("GET /api/schedules/{id}", RequireAuth(s, automationHandler.HandleGetSchedule))
	mux.HandleFunc("PUT /api/schedules/{id}", RequireAuth(s, automationHandler.HandleUpdateSchedule))
	mux.HandleFunc("POST /api/schedules/{id}/trigger", RequireAuth(s, automationHandler.HandleTriggerSchedule))
	mux.HandleFunc("DELETE /api/schedules/{id}", RequireAuth(s, automationHandler.HandleDeleteSchedule))

	// Protected Harnesses
	mux.HandleFunc("GET /api/harnesses", RequireAuth(s, automationHandler.HandleListHarnesses))
	mux.HandleFunc("POST /api/harnesses/{name}/update", RequireAuth(s, automationHandler.HandleUpdateHarness))

	// Protected Notification Settings (Telegram / Ntfy / Quiet Hours)
	mux.HandleFunc("GET /api/notifications/settings", RequireAuth(s, automationHandler.HandleGetNotificationSettings))
	mux.HandleFunc("PUT /api/notifications/settings", RequireAuth(s, automationHandler.HandleUpdateNotificationSettings))

	// Protected Repo Subagents (.claude/agents/*.md)
	mux.HandleFunc("GET /api/repos/{id}/claude-agents", RequireAuth(s, subagentsHandler.HandleList))
	mux.HandleFunc("POST /api/repos/{id}/claude-agents", RequireAuth(s, subagentsHandler.HandleSave))
	mux.HandleFunc("DELETE /api/repos/{id}/claude-agents/{name}", RequireAuth(s, subagentsHandler.HandleDelete))

	// Real-time WebSockets
	mux.HandleFunc("GET /api/events/ws", RequireAuth(s, wsHandler.HandleEventsWS))
	mux.HandleFunc("GET /api/terminal/ws", RequireAuth(s, wsHandler.HandleTerminalWS))

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

			// Fallback to index.html for SPA routing
			indexData, err := fs.ReadFile(webFS, "index.html")
			if err != nil {
				http.Error(w, "index.html not found", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexData)
		})
	}

	// Middleware chain: Recovery -> SecurityHeaders -> CSRFProtection -> Handler
	var handler http.Handler = mux
	handler = BodyLimitMiddleware(handler)
	handler = CSRFProtectionMiddleware(handler)
	handler = SecurityHeadersMiddleware(handler)
	handler = RecoveryMiddleware(logger, handler)

	httpServer := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadTimeout:       30 * time.Second,
	}

	return &Server{
		cfg:        cfg,
		store:      s,
		orch:       orch,
		logger:     logger,
		httpServer: httpServer,
		mux:        mux,
		webFS:      webFS,
		ws:         wsHandler,
	}, nil
}

func (s *Server) Orchestrator() *orchestrator.Orchestrator {
	return s.orch
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func (s *Server) Start(ctx context.Context) error {
	if err := s.orch.Start(ctx); err != nil {
		return fmt.Errorf("start orchestrator: %w", err)
	}
	s.orch.StartSchedulerLoop(ctx, 1*time.Minute)

	done := make(chan struct{})
	shutdownDone := make(chan struct{})
	defer close(done)
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.Shutdown(shutdownCtx); err != nil {
				s.logger.Error("HTTP shutdown failed", "error", err)
				_ = s.httpServer.Close()
			}
		case <-done:
		}
	}()
	s.logger.Info("Starting HTTP server", "addr", s.cfg.BindAddr)
	err := s.httpServer.ListenAndServe()
	if ctx.Err() != nil {
		<-shutdownDone
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.ws != nil {
		s.ws.Close()
	}
	var httpErr error
	if s.httpServer != nil {
		httpErr = s.httpServer.Shutdown(ctx)
	}
	var orchErr error
	if s.orch != nil {
		orchErr = s.orch.Shutdown(ctx)
	}
	return errors.Join(httpErr, orchErr)
}
