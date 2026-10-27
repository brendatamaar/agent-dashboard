package runtime_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/runtime/docker"
	"agent-dashboard/internal/runtime/host"
)

func runRuntimeContractTest(t *testing.T, r runtime.Runtime) {
	ctx := context.Background()

	// 1. Initial list empty or baseline
	initial, err := r.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}

	// 2. Create session
	spec := runtime.RuntimeSpec{
		SessionName: "test-session-1",
		WorkDir:     os.TempDir(),
		Command:     []string{"sleep", "10"},
	}
	handle, err := r.Create(ctx, spec)
	if err != nil {
		t.Fatalf("Create session error: %v", err)
	}
	defer func() {
		_ = r.Destroy(ctx, handle)
	}()
	if string(handle) != "test-session-1" {
		t.Fatalf("unexpected handle %s", handle)
	}

	// 3. List contains session
	sessions, err := r.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
	if len(sessions) != len(initial)+1 {
		t.Fatalf("expected %d sessions, got %d", len(initial)+1, len(sessions))
	}

	// 4. Inspect running container/session
	info, err := r.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}
	if info.Status != "running" {
		t.Fatalf("expected running status, got %s", info.Status)
	}
	if info.OOMKilled {
		t.Fatalf("expected OOMKilled false, got true")
	}

	// 5. Attach
	pty, err := r.Attach(ctx, handle)
	if err != nil {
		t.Fatalf("Attach error: %v", err)
	}
	if err := pty.Resize(30, 100); err != nil {
		t.Fatalf("Resize error: %v", err)
	}
	_ = pty.Close()

	// 6. Stats
	stats, err := r.Stats(ctx, handle)
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.MemoryBytes == 0 {
		t.Logf("Warning: 0 memory bytes returned in stats")
	}

	// 7. Destroy
	if err := r.Destroy(ctx, handle); err != nil {
		t.Fatalf("Destroy error: %v", err)
	}

	after, err := r.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions after error: %v", err)
	}
	if len(after) != len(initial) {
		t.Fatalf("expected session count to return to %d, got %d", len(initial), len(after))
	}
}

func TestFakeRuntimeContract(t *testing.T) {
	fake := runtime.NewFakeRuntime()
	runRuntimeContractTest(t, fake)
}

func TestHostRuntimeContract(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("skipping host runtime contract test: tmux not found")
	}
	h := host.NewHostRuntime()
	runRuntimeContractTest(t, h)
}

func TestDockerRuntimeContract(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipping docker runtime contract test: docker binary not found")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("skipping docker runtime contract test: docker daemon not accessible")
	}
	if err := exec.Command("docker", "image", "inspect", "agentd/sandbox:latest").Run(); err != nil {
		t.Skip("skipping docker runtime contract test: agentd/sandbox:latest image not found locally")
	}
	d := docker.NewDockerRuntime()
	runRuntimeContractTest(t, d)
}
