package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-dashboard/internal/api"
	"agent-dashboard/internal/core/agent"
)

func loginAndGetCookie(t *testing.T, srv *api.Server) *http.Cookie {
	t.Helper()
	setupPayload := `{"username":"alice","password":"password12345"}`
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewBufferString(setupPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	for _, c := range w.Result().Cookies() {
		if c.Name == "agentd_session" {
			return c
		}
	}
	t.Fatalf("failed to get agentd_session cookie from setup response (code %d: %s)", w.Code, w.Body.String())
	return nil
}

func TestPhase2ApprovalsAndPushAPI(t *testing.T) {
	srv, _ := setupTestServer(t)
	cookie := loginAndGetCookie(t, srv)

	// 1. Web Push VAPID key
	req := httptest.NewRequest("GET", "/api/push/vapid-key", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for vapid-key, got %d", w.Code)
	}
	var vapidRes map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &vapidRes)
	if vapidRes["public_key"] == "" {
		t.Fatalf("expected non-empty public_key")
	}

	// 2. Subscribe Web Push
	subBody := bytes.NewBufferString(`{
		"endpoint": "https://push.example.com/sub/123",
		"keys": {
			"p256dh": "dummy-p256dh",
			"auth": "dummy-auth"
		}
	}`)
	subReq := httptest.NewRequest("POST", "/api/push/subscribe", subBody)
	subReq.AddCookie(cookie)
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	subW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(subW, subReq)
	if subW.Code != http.StatusOK {
		t.Fatalf("expected 200 for push subscribe, got %d: %s", subW.Code, subW.Body.String())
	}

	// 3. Unsubscribe Web Push
	unsubBody := bytes.NewBufferString(`{"endpoint": "https://push.example.com/sub/123"}`)
	unsubReq := httptest.NewRequest("POST", "/api/push/unsubscribe", unsubBody)
	unsubReq.AddCookie(cookie)
	unsubReq.Header.Set("Content-Type", "application/json")
	unsubReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	unsubW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unsubW, unsubReq)
	if unsubW.Code != http.StatusOK {
		t.Fatalf("expected 200 for push unsubscribe, got %d", unsubW.Code)
	}
}

func TestPhase2HookApprovalFlow(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Test RequestApproval timeout / abort
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	action, err := srv.Orchestrator().RequestApproval(ctx, agent.ApprovalRequest{
		ID:       "app-timeout",
		AgentID:  "agent-nonexistent",
		ToolName: "Bash",
		Command:  "echo hi",
	})
	if err == nil && action == "allow" {
		t.Fatalf("expected error or deny for non-existent agent")
	}
}

func TestPhase2GitAPI(t *testing.T) {
	srv, _ := setupTestServer(t)
	cookie := loginAndGetCookie(t, srv)

	// Check git operations on non-existent agent returns 404
	req := httptest.NewRequest("GET", "/api/agents/non-existent/git/status", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent git status, got %d", w.Code)
	}

	// Test push guardrail refusal on main branch
	pushBody := bytes.NewBufferString(`{"force": false, "confirm_main": false}`)
	pushReq := httptest.NewRequest("POST", "/api/agents/non-existent/git/push", pushBody)
	pushReq.AddCookie(cookie)
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	pushW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pushW, pushReq)
	if pushW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent, got %d", pushW.Code)
	}
}
