package runtime_test

import (
	rt "agent-dashboard/internal/runtime"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestApprovalHookBridgeAllowsDeniesAndRetriesStableID(t *testing.T) {
	var mu sync.Mutex
	var ids []string
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hook-private" {
			t.Error("missing scoped bearer token")
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["agent_id"] != "agent-one" || payload["tool_name"] != "Bash" || payload["command"] != "echo safe" {
			t.Errorf("wrong hook payload %v", payload)
		}
		mu.Lock()
		defer mu.Unlock()
		id, _ := payload["request_id"].(string)
		ids = append(ids, id)
		attempt++
		if attempt == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"action":"allow"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	script := filepath.Join(dir, "supervisor.py")
	_ = os.WriteFile(script, rt.SupervisorScript(), 0600)
	cmd := pythonCommand(t, script, "hook")
	cmd.Env = append(os.Environ(), "AGENT_DASHBOARD_AGENT_ID=agent-one", "AGENT_DASHBOARD_HOOK_TOKEN=hook-private", "AGENT_DASHBOARD_HOOK_URL="+server.URL, "AGENT_DASHBOARD_RUNTIME=host")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"echo safe"}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %s %v", out, err)
	}
	var result struct {
		Output struct {
			Decision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &result); err != nil || result.Output.Decision != "allow" {
		t.Fatalf("hook decision: %s %v", out, err)
	}
	mu.Lock()
	if len(ids) != 2 || ids[0] != ids[1] || len(ids[0]) != 36 {
		t.Errorf("retry changed approval identity: %v", ids)
	}
	mu.Unlock()
	deny := pythonCommand(t, script, "hook")
	deny.Env = append(os.Environ(), "AGENT_DASHBOARD_RUNTIME=docker", "AGENT_DASHBOARD_AGENT_ID=agent-one", "AGENT_DASHBOARD_HOOK_TOKEN=hook-private", "AGENT_DASHBOARD_HOOK_URL="+server.URL)
	deny.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{}}`)
	out, err = deny.Output()
	if err != nil || !bytes.Contains(out, []byte(`"permissionDecision": "deny"`)) || !bytes.Contains(out, []byte("Tailscale")) {
		t.Fatalf("Docker loopback did not fail closed: %s %v", out, err)
	}
}
func TestFakeRuntimeReplayExitAndLiteralInput(t *testing.T) {
	f := rt.NewFakeRuntime()
	ctx := context.Background()
	h, err := f.Create(ctx, rt.RuntimeSpec{SessionName: "one"})
	if err != nil {
		t.Fatal(err)
	}
	f.SeedOutput(h, "durable output\n")
	f.Complete(h, 23)
	for i := 0; i < 2; i++ {
		r, err := f.Output(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r)
		_ = r.Close()
		if string(data) != "durable output\n" {
			t.Fatal("output replay changed")
		}
	}
	r, err := f.Exec(ctx, h, []string{"C-c $(echo keep)  two spaces\n"})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if f.Inputs(h)[0] != "C-c $(echo keep)  two spaces\n" {
		t.Fatal("literal input changed")
	}
	info, err := f.Inspect(ctx, h)
	if err != nil || info.ExitCode != 23 || info.Status != "exited" {
		t.Fatalf("exit lost: %+v %v", info, err)
	}
}
func TestSupervisorCopiesStayIdentical(t *testing.T) {
	copy, err := os.ReadFile(filepath.Join("..", "..", "scripts", "docker", "supervisor.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copy, rt.SupervisorScript()) {
		t.Fatal("Docker supervisor differs from embedded host supervisor")
	}
}
