package api

import (
	"agent-dashboard/internal/runtime"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-dashboard/internal/core/auth"
	"agent-dashboard/internal/shell/logger"
	"agent-dashboard/internal/store"
	"github.com/coder/websocket"
	"github.com/go-webauthn/webauthn/webauthn"
)

func securityServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "test.db")
	srv, err := NewServer(cfg, st, logger.New(io.Discard, slog.LevelDebug), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	user := &store.User{ID: "owner", Username: "alice", MasterSalt: []byte("0123456789abcdef"), IsSetupComplete: true}
	if err = st.CreateInitialUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	token, err := st.CreateSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, token
}
func TestPeerIPThrottlingCannotBeForged(t *testing.T) {
	limiter := NewRateLimiterMiddleware(auth.DefaultRateLimiterConfig())
	calls := 0
	handler := limiter.Limit(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusUnauthorized) })
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = "[::ffff:127.0.0.1]:" + strings.Repeat("1", i+1)
		r.Header.Set("X-Forwarded-For", strings.Repeat("9", i+1))
		w := httptest.NewRecorder()
		handler(w, r)
		if i == 5 && w.Code != 429 {
			t.Fatalf("changed source port/forwarded header bypassed throttle: %d", w.Code)
		}
	}
	if calls != 5 {
		t.Fatalf("expected 5 admitted attempts, got %d", calls)
	}
}
func TestPasskeyChallengesBoundExpireAndBindIdentity(t *testing.T) {
	a := &AuthHandler{waSessions: make(map[string]passkeyChallenge)}
	for i := 0; i < maxPasskeyChallenges; i++ {
		if !a.saveChallenge(string(rune(i)), &webauthn.SessionData{}, "alice", "login") {
			t.Fatal("capacity reached early")
		}
	}
	if a.saveChallenge("overflow", &webauthn.SessionData{}, "alice", "login") {
		t.Fatal("unbounded challenge allocation")
	}
	v := a.waSessions[string(rune(0))]
	v.expires = time.Now().Add(-time.Second)
	a.waSessions[string(rune(0))] = v
	if _, ok := a.takeChallenge(string(rune(0)), "alice", "login"); ok {
		t.Fatal("expired challenge accepted")
	}
	if _, ok := a.takeChallenge(string(rune(1)), "bob", "login"); ok {
		t.Fatal("challenge accepted for other user")
	}
	if _, ok := a.takeChallenge(string(rune(2)), "alice", "registration"); ok {
		t.Fatal("challenge accepted for other ceremony")
	}
	if _, ok := a.takeChallenge(string(rune(3)), "alice", "login"); !ok {
		t.Fatal("valid challenge rejected")
	}
	if _, ok := a.takeChallenge(string(rune(3)), "alice", "login"); ok {
		t.Fatal("challenge replay accepted")
	}
}
func TestHookRequiresScopedCredentials(t *testing.T) {
	srv, _, _ := securityServer(t)
	r := httptest.NewRequest("POST", "/api/internal/hooks/approval", strings.NewReader(`{"agent_id":"other-agent","tool_name":"Bash"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthenticated hook got %d: %s", w.Code, w.Body.String())
	}
}
func TestAPIBodyLimit(t *testing.T) {
	handler := BodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("oversized request reached handler") }))
	r := httptest.NewRequest("POST", "/api/secrets", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("got %d", w.Code)
	}
}
func TestWebSocketOriginRevocationAndShutdown(t *testing.T) {
	srv, st, token := securityServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/events/ws"
	headers := http.Header{"Origin": []string{"https://attacker.example"}, "Cookie": []string{"agentd_session=" + token}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil || resp.StatusCode != 403 {
		t.Fatalf("cross-origin socket accepted: %v", err)
	}
	headers.Set("Origin", srv.cfg.RPOrigin)
	conn, _, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err = st.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("revoked session remained connected")
	}
	token, err = st.CreateSession(ctx, "owner", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	headers.Set("Cookie", "agentd_session="+token)
	conn, _, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err = srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("socket survived server shutdown")
	}
}

func TestEventHistoryQueryBounds(t *testing.T) {
	srv, _, token := securityServer(t)
	for _, query := range []string{"after_seq=garbage", "after_seq=-1", "limit=0", "limit=501", "limit=invalid"} {
		r := httptest.NewRequest("GET", "/api/agents/unused/events?"+query, nil)
		r.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid query %q got %d", query, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/agents/unused/events?after_seq=0&limit=1", nil)
	r.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("valid empty page: %d %s", w.Code, w.Body.String())
	}
}

type countingRuntime struct {
	*runtime.FakeRuntime
	attaches atomic.Int32
}

func (r *countingRuntime) Attach(ctx context.Context, h runtime.Handle) (runtime.PTY, error) {
	r.attaches.Add(1)
	return r.FakeRuntime.Attach(ctx, h)
}
func TestTerminalSelectsHostAndAudits(t *testing.T) {
	srv, st, token := securityServer(t)
	host := &countingRuntime{FakeRuntime: runtime.NewFakeRuntime()}
	docker := &countingRuntime{FakeRuntime: runtime.NewFakeRuntime()}
	srv.orch.RegisterRuntime("host", host)
	srv.orch.RegisterRuntime("docker", docker)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	headers := http.Header{"Origin": []string{srv.cfg.RPOrigin}, "Cookie": []string{"agentd_session=" + token}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/terminal/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	_, _, _ = conn.Read(ctx)
	if host.attaches.Load() != 1 || docker.attaches.Load() != 0 {
		t.Fatalf("terminal runtime selection: host=%d docker=%d", host.attaches.Load(), docker.attaches.Load())
	}
	logs, err := st.GetAuditLogs(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range logs {
		if entry.Action == "terminal_attach" && entry.Actor == "alice" {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal attach omitted audit entry")
	}
}

func TestAPIAuditRedactsDynamicAndKnownSecrets(t *testing.T) {
	srv, st, token := securityServer(t)
	custom := "private-audit-canary-without-vendor-prefix"
	req := httptest.NewRequest("POST", "/api/secrets", strings.NewReader(`{"kind":"github","name":"audit_test","value":"`+custom+`"}`))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.AddCookie(&http.Cookie{Name: "agentd_session", Value: token})
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("save secret: %d %s", res.Code, res.Body.String())
	}
	known := "ghp_" + strings.Repeat("A", 36)
	req = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"`+custom+" "+known+`","password":"invalid-password"}`))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatalf("login audit fixture: %d %s", res.Code, res.Body.String())
	}
	if err := st.WriteAudit(context.Background(), custom, known, custom, known+custom); err != nil {
		t.Fatal(err)
	}
	logs, err := st.GetAuditLogs(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		for _, value := range []string{entry.Actor, entry.Action, entry.Target, entry.Result} {
			if strings.Contains(value, custom) || strings.Contains(value, known) {
				t.Fatalf("audit row leaked stored credential: %+v", entry)
			}
		}
	}
}
