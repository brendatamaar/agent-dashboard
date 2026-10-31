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
	"agent-dashboard/internal/core/notification"
	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/notifier"
	"agent-dashboard/internal/store"
)

func setupPhase4Server(t *testing.T) (*api.Server, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	cfg := api.Config{
		BindAddr:         "127.0.0.1:0",
		DataDir:          tmpDir,
		MasterPassphrase: "test-passphrase-master-1234",
	}

	srv, err := api.NewServer(cfg, st, nil, nil)
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}

	// Create test user and session for auth
	user := store.User{
		ID:              "user-phase4",
		Username:        "admin",
		PasswordHash:    "dummy",
		MasterSalt:      []byte("1234567890123456"),
		IsSetupComplete: true,
	}
	if err := st.CreateUser(ctx, &user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	token, err := st.CreateSession(ctx, user.ID, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	srv.Orchestrator().RegisterRuntime("docker", runtime.NewFakeRuntime())
	srv.Orchestrator().RegisterRuntime("host", runtime.NewFakeRuntime())
	if n := srv.Orchestrator().Notifier(); n != nil {
		n.SetHTTPClient(&http.Client{Transport: noNotificationTransport{}})
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return srv, st, token
}

func TestPhase4AutomationAndSubagentsAPI(t *testing.T) {
	srv, st, token := setupPhase4Server(t)
	defer func() { _ = st.Close() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	handler := srv.Handler()

	doAuthReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reqBody = bytes.NewReader(data)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		req := httptest.NewRequest(method, path, reqBody)
		req.AddCookie(&http.Cookie{
			Name:  "agentd_session",
			Value: token,
		})
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// 1. Templates API
	res := doAuthReq("GET", "/api/templates", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/templates failed: %d", res.Code)
	}

	createTmplBody := map[string]string{
		"name":    "Dep Update",
		"harness": "codex",
		"prompt":  "upgrade packages and run tests",
	}
	res = doAuthReq("POST", "/api/templates", createTmplBody)
	if res.Code != http.StatusCreated {
		t.Fatalf("POST /api/templates failed: %d, body: %s", res.Code, res.Body.String())
	}
	var createdTmpl store.Template
	_ = json.Unmarshal(res.Body.Bytes(), &createdTmpl)

	// 2. Schedules API
	// First add a repo
	repo := store.Repo{
		ID:        "repo-p4",
		Name:      "p4-repo",
		RemoteURL: "https://github.com/example/p4.git",
		Path:      setupTestGitRepo(t),
	}
	_ = st.CreateRepo(context.Background(), &repo)

	createSchBody := map[string]any{
		"template_id": createdTmpl.ID,
		"repo_id":     repo.ID,
		"cron":        "0 3 * * *",
		"enabled":     true,
	}
	res = doAuthReq("POST", "/api/schedules", createSchBody)
	if res.Code != http.StatusCreated {
		t.Fatalf("POST /api/schedules failed: %d, body: %s", res.Code, res.Body.String())
	}
	var createdSch store.Schedule
	_ = json.Unmarshal(res.Body.Bytes(), &createdSch)

	invalidSchedule := createdSch
	invalidSchedule.Cron = "definitely-not-cron"
	res = doAuthReq("PUT", "/api/schedules/"+createdSch.ID, invalidSchedule)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid schedule update accepted: %d", res.Code)
	}
	saved, err := st.GetSchedule(context.Background(), createdSch.ID)
	if err != nil || saved.Cron != createdSch.Cron {
		t.Fatal("invalid cron update changed persisted schedule")
	}
	// Trigger schedule
	res = doAuthReq("POST", "/api/schedules/"+createdSch.ID+"/trigger", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/schedules/.../trigger failed: %d", res.Code)
	}

	// 3. Harnesses API
	res = doAuthReq("GET", "/api/harnesses", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/harnesses failed: %d", res.Code)
	}
	var harnesses []any
	_ = json.Unmarshal(res.Body.Bytes(), &harnesses)
	if len(harnesses) < 4 {
		t.Errorf("expected at least 4 harnesses, got %d", len(harnesses))
	}

	// 4. Notification Settings API
	notifSettings := notifier.ChannelSettings{
		Telegram: notifier.TelegramConfig{
			Enabled:  true,
			BotToken: "tok",
			ChatID:   "chat1",
		},
		Ntfy: notifier.NtfyConfig{
			Enabled: true,
			Topic:   "test-top",
		},
		QuietHours: notification.QuietHoursConfig{
			Enabled:   true,
			StartHour: 22,
			EndHour:   7,
		},
	}
	res = doAuthReq("PUT", "/api/notifications/settings", notifSettings)
	if res.Code != http.StatusOK {
		t.Fatalf("PUT /api/notifications/settings failed: %d", res.Code)
	}

	res = doAuthReq("GET", "/api/notifications/settings", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/notifications/settings failed: %d", res.Code)
	}

	// 5. Repo Subagents (.claude/agents/*.md)
	subDef := map[string]string{
		"name":    "code-reviewer",
		"content": "# Code Reviewer Agent\nInspect PR diffs.",
	}
	res = doAuthReq("POST", "/api/repos/"+repo.ID+"/claude-agents", subDef)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/repos/.../claude-agents failed: %d, body: %s", res.Code, res.Body.String())
	}

	res = doAuthReq("GET", "/api/repos/"+repo.ID+"/claude-agents", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/repos/.../claude-agents failed: %d", res.Code)
	}
	var subList []map[string]string
	_ = json.Unmarshal(res.Body.Bytes(), &subList)
	if len(subList) != 1 || subList[0]["name"] != "code-reviewer" {
		t.Errorf("unexpected subagent list: %v", subList)
	}

	res = doAuthReq("DELETE", "/api/repos/"+repo.ID+"/claude-agents/code-reviewer", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("DELETE /api/repos/.../claude-agents failed: %d", res.Code)
	}
}
