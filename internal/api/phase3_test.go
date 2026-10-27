package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/api"
	"agent-dashboard/internal/core/repo"
	"agent-dashboard/internal/core/system"
	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"
)

func setupPhase3Server(t *testing.T) (*httptest.Server, *store.Store, string) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "phase3_test.db")
	ctx := context.Background()

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	salt, _ := crypto.GenerateSalt()
	u := &store.User{
		ID:              "user-phase3",
		Username:        "admin",
		PasswordHash:    "dummy_hash",
		MasterSalt:      salt,
		IsSetupComplete: true,
	}
	_ = s.CreateUser(ctx, u)
	token, _ := s.CreateSession(ctx, u.ID, 1*time.Hour)

	cfg := api.Config{
		DataDir:          tempDir,
		MasterPassphrase: "test-passphrase",
	}

	srv, err := api.NewServer(cfg, s, nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	return ts, s, token
}

func TestPhase3RepoSettingsAndAutoApprove(t *testing.T) {
	ts, s, token := setupPhase3Server(t)
	defer ts.Close()
	defer s.Close()
	ctx := context.Background()

	// Create test repo
	r := &store.Repo{
		ID:            "repo-p3-1",
		Name:          "test-p3-repo",
		RemoteURL:     "https://github.com/brendatama/test-p3-repo.git",
		Path:          filepath.Join(t.TempDir(), "test-p3-repo"),
		DefaultBranch: "main",
		SettingsJSON:  "{}",
	}
	_ = s.CreateRepo(ctx, r)

	// 1. GET initial settings (default)
	req, _ := http.NewRequest("GET", ts.URL+"/api/repos/repo-p3-1/settings", nil)
	req.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET settings error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	var set repo.RepoSettings
	_ = json.NewDecoder(resp.Body).Decode(&set)
	if set.Runtime != "docker" {
		t.Fatalf("expected default runtime docker, got %s", set.Runtime)
	}

	// 2. PUT invalid settings: host runtime with auto_approve=true must be rejected (400)
	invalidSet := repo.RepoSettings{
		Runtime:     "host",
		AutoApprove: true,
	}
	body, _ := json.Marshal(invalidSet)
	req, _ = http.NewRequest("PUT", ts.URL+"/api/repos/repo-p3-1/settings", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for auto_approve on host, got %d", resp.StatusCode)
	}

	// 3. PUT valid settings: docker runtime with auto_approve=true succeeds (200)
	validSet := repo.RepoSettings{
		Runtime:      "docker",
		Image:        "agentd/sandbox:custom",
		SetupCommand: "npm ci",
		AutoApprove:  true,
	}
	body, _ = json.Marshal(validSet)
	req, _ = http.NewRequest("PUT", ts.URL+"/api/repos/repo-p3-1/settings", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT valid settings error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify persistence in DB
	updatedRepo, _ := s.GetRepo(ctx, "repo-p3-1")
	var savedSet repo.RepoSettings
	_ = json.Unmarshal([]byte(updatedRepo.SettingsJSON), &savedSet)
	if !savedSet.AutoApprove || savedSet.Image != "agentd/sandbox:custom" {
		t.Fatalf("settings not persisted properly: %+v", savedSet)
	}
}

func TestPhase3TelemetrySystem(t *testing.T) {
	ts, s, token := setupPhase3Server(t)
	defer ts.Close()
	defer s.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/system", nil)
	req.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/system error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var usage system.ResourceUsage
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		t.Fatalf("failed to decode system usage: %v", err)
	}

	// Verify telemetry has memory and agents slice
	if usage.MemoryTotalBytes == 0 {
		t.Fatalf("expected non-zero MemoryTotalBytes")
	}
	if usage.Agents == nil {
		t.Fatalf("expected non-nil Agents slice in system telemetry")
	}
}
