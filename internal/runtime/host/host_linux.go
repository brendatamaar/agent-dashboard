//go:build !windows

package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v3/process"

	"agent-dashboard/internal/runtime"
)

type ptyWrapper struct {
	f *os.File
}

func (p *ptyWrapper) Read(b []byte) (int, error) {
	return p.f.Read(b)
}

func (p *ptyWrapper) Write(b []byte) (int, error) {
	return p.f.Write(b)
}

func (p *ptyWrapper) Close() error {
	return p.f.Close()
}

func (p *ptyWrapper) Resize(rows, cols uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (h *HostRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	if spec.SessionName == "" {
		return "", fmt.Errorf("session name is required")
	}

	args := []string{"new-session", "-d", "-s", spec.SessionName}
	if spec.WorkDir != "" {
		args = append(args, "-c", spec.WorkDir)
	}

	// Append command if specified
	if len(spec.Command) > 0 {
		args = append(args, spec.Command...)
	}

	cmd := exec.CommandContext(ctx, h.tmuxBinary, args...)
	// Ensure complete environment including ~/.local/bin and /usr/local/bin in PATH
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	curPath := envMap["PATH"]
	homeDir, _ := os.UserHomeDir()
	extraPaths := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	if homeDir != "" {
		extraPaths = append([]string{filepath.Join(homeDir, ".local", "bin")}, extraPaths...)
	}
	for _, p := range extraPaths {
		if !strings.Contains(curPath, p) {
			curPath = p + ":" + curPath
		}
	}
	envMap["PATH"] = curPath
	for k, v := range spec.Env {
		envMap[k] = v
	}
	cmd.Env = make([]string, 0, len(envMap))
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux new-session failed: %s: %w", stderr.String(), err)
	}

	return runtime.Handle(spec.SessionName), nil
}

func (h *HostRuntime) Exec(ctx context.Context, handle runtime.Handle, cmdArgs []string) (io.ReadCloser, error) {
	// For sending raw input keys to the tmux pane
	joined := strings.Join(cmdArgs, " ")
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "send-keys", "-t", string(handle), joined, "Enter")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tmux send-keys failed: %w", err)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (h *HostRuntime) Attach(ctx context.Context, handle runtime.Handle) (runtime.PTY, error) {
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "attach-session", "-t", string(handle))
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to attach pty to tmux session %q: %w", handle, err)
	}
	return &ptyWrapper{f: ptmx}, nil
}

func (h *HostRuntime) Stats(ctx context.Context, handle runtime.Handle) (runtime.Usage, error) {
	// Get PID of pane inside tmux
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "list-panes", "-t", string(handle), "-F", "#{pane_pid}")
	out, err := cmd.Output()
	if err != nil {
		return runtime.Usage{}, fmt.Errorf("failed to list tmux panes: %w", err)
	}

	pidStr := strings.TrimSpace(string(out))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return runtime.Usage{}, fmt.Errorf("invalid pane pid %q: %w", pidStr, err)
	}

	p, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return runtime.Usage{}, fmt.Errorf("process not found: %w", err)
	}

	cpuPercent, _ := p.CPUPercentWithContext(ctx)
	memInfo, _ := p.MemoryInfoWithContext(ctx)

	var memBytes uint64
	if memInfo != nil {
		memBytes = memInfo.RSS
	}

	return runtime.Usage{
		CPUPercent:  cpuPercent,
		MemoryBytes: memBytes,
	}, nil
}

func (h *HostRuntime) Destroy(ctx context.Context, handle runtime.Handle) error {
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "kill-session", "-t", string(handle))
	_ = cmd.Run() // Ignore error if already killed
	return nil
}

func (h *HostRuntime) ListSessions(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		// If tmux server is not running, list-sessions returns error with "no server running"
		return nil, nil
	}

	var sessions []string
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			sessions = append(sessions, l)
		}
	}
	return sessions, nil
}

func (h *HostRuntime) Inspect(ctx context.Context, handle runtime.Handle) (runtime.ContainerInfo, error) {
	cmd := exec.CommandContext(ctx, h.tmuxBinary, "has-session", "-t", string(handle))
	if err := cmd.Run(); err != nil {
		return runtime.ContainerInfo{Status: "stopped", ExitCode: 0, OOMKilled: false}, nil
	}
	return runtime.ContainerInfo{Status: "running", ExitCode: 0, OOMKilled: false}, nil
}
