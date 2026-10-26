package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-dashboard/internal/core/bind"
)

// Config holds runtime configuration options.
type Config struct {
	BindAddr         string        `json:"bind_addr"`
	AllowPublicBind  bool          `json:"allow_public_bind"`
	DataDir          string        `json:"data_dir"`
	DBPath           string        `json:"db_path"`
	MasterPassphrase string        `json:"master_passphrase"`
	SessionDuration  time.Duration `json:"session_duration"`
	RPID             string        `json:"rp_id"`
	RPOrigin         string        `json:"rp_origin"`
	LogLevel         string        `json:"log_level"`
}

// DefaultConfig provides secure defaults for agent-dashboard.
func DefaultConfig() Config {
	dataDir := os.Getenv("AGENTD_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			dataDir = filepath.Join(home, ".agent-dashboard")
		} else {
			dataDir = ".agent-dashboard"
		}
	}

	return Config{
		BindAddr:         "127.0.0.1:8080",
		AllowPublicBind:  false,
		DataDir:          dataDir,
		DBPath:           filepath.Join(dataDir, "agent-dashboard.db"),
		MasterPassphrase: "",
		SessionDuration:  24 * time.Hour,
		RPID:             "localhost",
		RPOrigin:         "http://localhost:8080",
		LogLevel:         "info",
	}
}

// LoadConfig merges default config with optional file and environment variables.
func LoadConfig(configPath string) (Config, error) {
	cfg := DefaultConfig()

	// Load from file if specified and exists
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return cfg, fmt.Errorf("failed to read config file: %w", err)
		}
		if err == nil {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("failed to parse config file: %w", err)
			}
		}
	}

	// Environment variable overrides
	if v := os.Getenv("AGENTD_BIND"); v != "" {
		cfg.BindAddr = v
	}
	if v := os.Getenv("AGENTD_ALLOW_PUBLIC_BIND"); v != "" {
		cfg.AllowPublicBind = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("AGENTD_DATA_DIR"); v != "" {
		cfg.DataDir = v
		cfg.DBPath = filepath.Join(v, "agent-dashboard.db")
	}
	if v := os.Getenv("AGENTD_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("AGENTD_MASTER_PASSPHRASE"); v != "" {
		cfg.MasterPassphrase = v
	}
	if v := os.Getenv("AGENTD_RP_ID"); v != "" {
		cfg.RPID = v
	}
	if v := os.Getenv("AGENTD_RP_ORIGIN"); v != "" {
		cfg.RPOrigin = v
	}
	if v := os.Getenv("AGENTD_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}

	// Validate bind address against policy
	if err := bind.ValidateBindAddress(cfg.BindAddr, cfg.AllowPublicBind); err != nil {
		return cfg, fmt.Errorf("bind policy violation: %w", err)
	}

	return cfg, nil
}
