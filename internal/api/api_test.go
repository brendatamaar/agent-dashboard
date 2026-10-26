package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/api"
	"agent-dashboard/internal/shell/logger"
	"agent-dashboard/internal/store"

	"github.com/pquerna/otp/totp"
)

func setupTestServer(t *testing.T) (*api.Server, *store.Store) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api.db")

	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})

	cfg := api.DefaultConfig()
	cfg.BindAddr = "127.0.0.1:0"
	cfg.DataDir = tempDir
	cfg.DBPath = dbPath
	cfg.MasterPassphrase = "master-test-key-2026"

	l := logger.New(io.Discard, slog.LevelInfo)
	srv, err := api.NewServer(cfg, s, l, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return srv, s
}

func TestHealthz(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["status"] != "ok" {
		t.Fatalf("expected status: ok, got %s", res["status"])
	}
}

func TestCSRFProtection(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Mutating request without X-Requested-With should be rejected
	body := bytes.NewBufferString(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden without CSRF header, got %d", w.Code)
	}
}

func TestAuthFlowAndProtectedEndpoints(t *testing.T) {
	srv, _ := setupTestServer(t)

	// 1. Setup Status initially true (setup needed)
	req := httptest.NewRequest("GET", "/api/auth/setup/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var statusRes api.SetupStatusResponse
	_ = json.Unmarshal(w.Body.Bytes(), &statusRes)
	if !statusRes.SetupNeeded {
		t.Fatal("expected setup_needed = true")
	}

	// 2. Perform Setup
	setupPayload := `{"username":"alice","password":"password12345"}`
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewBufferString(setupPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("setup failed with code %d: %s", w.Code, w.Body.String())
	}

	var setupRes api.SetupInitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &setupRes); err != nil {
		t.Fatalf("failed to decode setup response: %v", err)
	}
	if setupRes.TOTPSecret == "" {
		t.Fatal("expected TOTP secret in response")
	}

	// Session cookie should be set from setup
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "agentd_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected agentd_session cookie set")
	}

	// 3. Access protected endpoint /api/auth/me using session
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/api/auth/me returned %d: %s", w.Code, w.Body.String())
	}

	var meRes map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &meRes)
	if meRes["username"] != "alice" {
		t.Fatalf("expected username alice, got %v", meRes["username"])
	}

	// 4. Access system telemetry
	req = httptest.NewRequest("GET", "/api/system", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/api/system returned %d: %s", w.Code, w.Body.String())
	}

	// 5. Create secret
	secretBody := `{"kind":"github","name":"github_token","value":"ghp_abcdef0123456789abcdef0123456789abcd"}`
	req = httptest.NewRequest("POST", "/api/secrets", bytes.NewBufferString(secretBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create secret failed with code %d: %s", w.Code, w.Body.String())
	}

	// 6. List secrets (ensure ciphertext is not returned)
	req = httptest.NewRequest("GET", "/api/secrets", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list secrets failed with code %d: %s", w.Code, w.Body.String())
	}
	var secretsList []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &secretsList)
	if len(secretsList) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secretsList))
	}
	if secretsList[0]["name"] != "github_token" {
		t.Fatalf("unexpected secret name: %v", secretsList[0]["name"])
	}
	if _, hasCiphertext := secretsList[0]["ciphertext"]; hasCiphertext {
		t.Fatal("secrets list leaked ciphertext field")
	}

	// 7. Check Audit Logs
	req = httptest.NewRequest("GET", "/api/audit-log", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("audit logs returned %d: %s", w.Code, w.Body.String())
	}
	var auditList []store.AuditEntry
	_ = json.Unmarshal(w.Body.Bytes(), &auditList)
	if len(auditList) == 0 {
		t.Fatal("expected audit logs to contain events")
	}

	// 8. Logout
	req = httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("logout failed with code %d", w.Code)
	}

	// Subsequent request with old session should fail
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", w.Code)
	}

	// 9. Login again with password + TOTP
	totpPasscode, err := totp.GenerateCode(setupRes.TOTPSecret, time.Now())
	if err != nil {
		t.Fatalf("failed to generate TOTP passcode: %v", err)
	}
	loginPayload, _ := json.Marshal(map[string]string{
		"username":  "alice",
		"password":  "password12345",
		"totp_code": totpPasscode,
	})
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(loginPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login failed with code %d: %s", w.Code, w.Body.String())
	}
}
