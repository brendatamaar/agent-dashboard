package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/api"
	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"
)

func createTestUserAndSession(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	salt, _ := crypto.GenerateSalt()
	user := &store.User{
		ID:              "user-admin",
		Username:        "admin",
		PasswordHash:    "hashed",
		MasterSalt:      salt,
		IsSetupComplete: true,
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	token, err := s.CreateSession(ctx, user.ID, 1*time.Hour)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	return token
}

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s", args, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@test.com")
	runGit("branch", "-M", "main")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644)
	runGit("add", "README.md")
	runGit("commit", "-m", "init")
	return dir
}

func TestReposAndAgentsAPI(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "api_test.db")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store failed: %v", err)
	}
	defer s.Close()

	sessionToken := createTestUserAndSession(t, s)

	cfg := api.Config{
		BindAddr:         "127.0.0.1:8080",
		DataDir:          tempDir,
		DBPath:           dbPath,
		MasterPassphrase: "test-passphrase",
	}

	server, err := api.NewServer(cfg, s, nil, nil)
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	handler := server.Handler()

	// Helper to send authenticated request
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Buffer
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewBuffer(b)
		} else {
			reqBody = bytes.NewBuffer(nil)
		}
		req := httptest.NewRequest(method, path, reqBody)
		req.AddCookie(&http.Cookie{Name: "agentd_session", Value: sessionToken})
		if method == http.MethodPost || method == http.MethodDelete {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Initially GET /api/repos is empty
	rec := doReq(http.MethodGet, "/api/repos", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Setup a local test git repo and add it to store
	repoDir := setupTestGitRepo(t)
	repoRec := &store.Repo{
		ID:            "test-repo-1",
		Name:          "local-repo",
		RemoteURL:     "https://github.com/brendatama/local-repo.git",
		Path:          repoDir,
		DefaultBranch: "main",
	}
	if err := s.CreateRepo(ctx, repoRec); err != nil {
		t.Fatalf("create repo in store failed: %v", err)
	}

	// 2. GET /api/repos should list repo with status
	rec = doReq(http.MethodGet, "/api/repos", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var repoList []api.RepoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &repoList); err != nil {
		t.Fatalf("unmarshal repos failed: %v", err)
	}
	if len(repoList) != 1 || repoList[0].Name != "local-repo" || repoList[0].CurrentBranch != "main" {
		t.Fatalf("unexpected repo list: %+v", repoList)
	}

	// 3. POST /api/agents (start agent)
	startAgentReq := map[string]string{
		"repo_id": "test-repo-1",
		"harness": "claude",
		"model":   "claude-sonnet-5-5",
		"mode":    "task",
		"prompt":  "Add tests",
	}
	rec = doReq(http.MethodPost, "/api/agents", startAgentReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var createdAgent store.AgentRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &createdAgent); err != nil {
		t.Fatalf("unmarshal created agent failed: %v", err)
	}
	if createdAgent.ID == "" {
		t.Fatalf("expected created agent to have ID")
	}

	// 4. GET /api/agents (list agents)
	rec = doReq(http.MethodGet, "/api/agents", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 5. POST /api/agents/{id}/message
	msgReq := map[string]string{"message": "follow up"}
	rec = doReq(http.MethodPost, "/api/agents/"+createdAgent.ID+"/message", msgReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for message, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. POST /api/agents/{id}/stop
	rec = doReq(http.MethodPost, "/api/agents/"+createdAgent.ID+"/stop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for stop, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. DELETE /api/agents/{id}
	rec = doReq(http.MethodDelete, "/api/agents/"+createdAgent.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for delete, got %d: %s", rec.Code, rec.Body.String())
	}
}
