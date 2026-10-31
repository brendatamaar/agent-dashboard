//go:build !windows

package runtime_test

import (
	rt "agent-dashboard/internal/runtime"
	"github.com/creack/pty"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractiveSupervisorPreservesTTYWithoutCapture(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "supervisor.py")
	log := filepath.Join(dir, "output")
	status := filepath.Join(dir, "exit")
	_ = os.WriteFile(script, rt.SupervisorScript(), 0600)
	child := pythonCommand(t, "-c", `import sys; print('TTY='+str(sys.stdin.isatty())+':'+str(sys.stdout.isatty())); sys.exit(9)`)
	cmd := pythonCommand(t, append([]string{script, "run", log, status, ""}, child.Args...)...)
	cmd.Env = append(os.Environ(), "AGENT_DASHBOARD_MODE=interactive")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(terminal)
	_ = terminal.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "TTY=True:True") {
		t.Fatalf("interactive stdio lost tty: %q", data)
	}
	captured, err := os.ReadFile(log)
	if err != nil || len(captured) != 0 {
		t.Fatalf("interactive output persisted: %q %v", captured, err)
	}
	exit, _ := os.ReadFile(status)
	if string(exit) != "9" {
		t.Fatalf("interactive exit lost: %s", exit)
	}
}
