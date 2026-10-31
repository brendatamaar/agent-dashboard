package runtime_test

import (
	rt "agent-dashboard/internal/runtime"
	"agent-dashboard/internal/runtime/docker"
	"agent-dashboard/internal/runtime/host"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func pythonCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("py"); err != nil {
			t.Skip("Python3 unavailable")
		}
		binary, err := exec.Command("py", "-3", "-c", "import sys; print(sys.executable)").Output()
		if err != nil {
			t.Fatalf("resolve Python3: %v", err)
		}
		return exec.Command(strings.TrimSpace(string(binary)), args...)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python3 unavailable")
	}
	return exec.Command("python3", args...)
}
func TestSupervisorRedactsBeforeDiskAndPreservesExit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "supervisor.py")
	log := filepath.Join(dir, "output")
	status := filepath.Join(dir, "exit")
	if err := os.WriteFile(script, rt.SupervisorScript(), 0600); err != nil {
		t.Fatal(err)
	}
	child := pythonCommand(t, "-c", `import os,sys; print(os.environ['CUSTOM_SECRET']); print('ghp_abcdefgh12345'); print('plain output'); sys.exit(7)`)
	args := []string{script, "run", log, status, ""}
	args = append(args, child.Args...)
	cmd := pythonCommand(t, args...)
	cmd.Env = append(os.Environ(), "CUSTOM_SECRET=custom-private-value")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("supervisor: %v %s", err, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("custom-private-value")) || bytes.Contains(data, []byte("ghp_abcdefgh")) || !bytes.Contains(data, []byte("plain output")) {
		t.Fatalf("redaction failed: %s", data)
	}
	exit, _ := os.ReadFile(status)
	if string(exit) != "7" {
		t.Fatalf("exit status changed: %q", exit)
	}
	replay, err := pythonCommand(t, script, "follow", log, status).Output()
	if err != nil || !bytes.Equal(replay, data) {
		t.Fatalf("durable replay differs: %s %v", replay, err)
	}
}
func TestSupervisorTranscriptLimitFailsWithoutTruncation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "supervisor.py")
	log := filepath.Join(dir, "output")
	status := filepath.Join(dir, "exit")
	// Run the actual cap branch using a small cap in an isolated script copy.
	data := bytes.Replace(rt.SupervisorScript(), []byte("MAX_LOG = 64 * 1024 * 1024"), []byte("MAX_LOG = 64"), 1)
	_ = os.WriteFile(script, data, 0600)
	child := pythonCommand(t, "-c", `print('x' * 1000)`)
	args := append([]string{script, "run", log, status, ""}, child.Args...)
	if out, err := pythonCommand(t, args...).CombinedOutput(); err != nil {
		t.Fatalf("supervisor: %v %s", err, out)
	}
	exit, _ := os.ReadFile(status)
	if string(exit) != "125" {
		t.Fatalf("overflow treated as success: %s", exit)
	}
	logged, _ := os.ReadFile(log)
	if bytes.Contains(logged, []byte("xxxxxxxx")) || !bytes.Contains(logged, []byte("size limit exceeded")) {
		t.Fatalf("overflow data persisted: %q", logged)
	}
}
func TestOutputReaderCancellationUnblocksRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := pythonCommand(t, "-c", "import time; time.sleep(60)")
	reader, err := rt.CommandReader(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, reader); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled output reader remains blocked")
	}
}
func TestAgentEnvironmentDoesNotInheritDashboardSecrets(t *testing.T) {
	t.Setenv("AGENTD_MASTER_KEY", "dashboard-master")
	t.Setenv("OPENAI_API_KEY", "unselected-provider")
	env := strings.Join(rt.AgentEnvironment(map[string]string{"ANTHROPIC_API_KEY": "selected-provider", "AGENTD_MASTER_KEY": "attempted-override"}), "\n")
	if strings.Contains(env, "dashboard-master") || strings.Contains(env, "unselected-provider") || strings.Contains(env, "attempted-override") || !strings.Contains(env, "selected-provider") {
		t.Fatalf("unsafe agent environment: %s", env)
	}
}
func TestWindowsRuntimeIsExplicitlyUnsupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only boundary")
	}
	for _, r := range []rt.Runtime{host.NewHostRuntime(), docker.NewDockerRuntime()} {
		if _, err := r.Create(context.Background(), rt.RuntimeSpec{SessionName: "test"}); !errors.Is(err, rt.ErrUnsupported) {
			t.Fatalf("%s simulated a production runtime: %v", r.Name(), err)
		}
	}
}
