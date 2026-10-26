package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"agent-dashboard/internal/api"
	"agent-dashboard/internal/core/bind"
	"agent-dashboard/internal/shell/logger"
	"agent-dashboard/internal/store"
	"agent-dashboard/web"
)

func main() {
	configPath := flag.String("config", "", "Path to optional config file")
	bindAddr := flag.String("bind", "", "Bind address (default 127.0.0.1:8080 or Tailscale IP)")
	allowPublic := flag.Bool("insecure-allow-public-bind", false, "Allow binding to public or wildcard (0.0.0.0) addresses")
	dataDir := flag.String("data-dir", "", "Directory for storing dashboard state and SQLite database")
	logLevelStr := flag.String("log-level", "", "Logging level (debug, info, warn, error)")
	flag.Parse()

	cfg, err := api.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	// CLI flag overrides
	if *bindAddr != "" {
		cfg.BindAddr = *bindAddr
	}
	if *allowPublic {
		cfg.AllowPublicBind = true
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
		cfg.DBPath = filepath.Join(*dataDir, "agent-dashboard.db")
	}
	if *logLevelStr != "" {
		cfg.LogLevel = *logLevelStr
	}

	// Re-verify bind address if flags changed it
	if err := bind.ValidateBindAddress(cfg.BindAddr, cfg.AllowPublicBind); err != nil {
		fmt.Fprintf(os.Stderr, "Bind policy violation: %v\n", err)
		os.Exit(1)
	}

	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	l := logger.New(os.Stdout, level, cfg.MasterPassphrase)

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		l.Error("failed to create data directory", "error", err, "path", cfg.DataDir)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		l.Error("failed to open database", "error", err, "path", cfg.DBPath)
		os.Exit(1)
	}
	defer s.Close()

	webFS, err := web.GetFS()
	if err != nil {
		l.Warn("failed to initialize web assets filesystem", "error", err)
	}

	srv, err := api.NewServer(cfg, s, l, webFS)
	if err != nil {
		l.Error("failed to initialize server", "error", err)
		os.Exit(1)
	}

	l.Info("agent-dashboard starting",
		"version", "0.1.0",
		"bind_addr", cfg.BindAddr,
		"allow_public_bind", cfg.AllowPublicBind,
		"db_path", cfg.DBPath,
	)

	if err := srv.Start(ctx); err != nil {
		l.Error("server exited with error", "error", err)
		os.Exit(1)
	}

	l.Info("agent-dashboard stopped cleanly")
}
