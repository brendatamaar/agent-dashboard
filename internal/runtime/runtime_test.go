package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

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
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	name := fmt.Sprintf("contract-%d", time.Now().UnixNano())
	spec := runtime.RuntimeSpec{
		SessionName: name,
		WorkDir:     dir,
		Command:     []string{"sleep", "10"},
	}
	handle, err := r.Create(ctx, spec)
	if err != nil {
		t.Fatalf("Create session error: %v", err)
	}
	defer func() {
		_ = r.Destroy(ctx, handle)
	}()
	if string(handle) != name {
		t.Fatalf("unexpected handle %s", handle)
	}

	if _, err := r.Create(ctx, spec); err == nil {
		t.Fatal("duplicate session accepted")
	}
	reader, err := r.Output(ctx, handle)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	_ = reader.Close()
	input := "C-c ; $(touch must-not-execute)  exact text"
	inputResult, err := r.Exec(ctx, handle, []string{input})
	if err != nil {
		t.Fatalf("literal input: %v", err)
	}
	_, _ = io.Copy(io.Discard, inputResult)
	_ = inputResult.Close()
	if observer, ok := r.(interface{ Inputs(runtime.Handle) []string }); ok {
		got := observer.Inputs(handle)
		if len(got) != 1 || got[0] != input {
			t.Fatalf("input changed: %q", got)
		}
	}
	if _, err := r.Inspect(ctx, runtime.Handle(name+"-missing")); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing inspection did not return ErrNotFound: %v", err)
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

	if err := r.Interrupt(ctx, handle); err != nil {
		t.Fatalf("Interrupt: %v", err)
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
	if os.Getenv("AGENTD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("Linux tmux integration requires AGENTD_RUNTIME_INTEGRATION=1")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("skipping host runtime contract test: tmux not found")
	}
	h := host.NewHostRuntimeWithLogDir(t.TempDir())
	runRuntimeContractTest(t, h)
}

func TestDockerRuntimeContract(t *testing.T) {
	if os.Getenv("AGENTD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("Linux Docker integration requires AGENTD_RUNTIME_INTEGRATION=1")
	}
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
