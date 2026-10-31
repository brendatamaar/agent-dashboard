//go:build !windows

package host

import (
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

func (h *HostRuntime) command(ctx context.Context, handle runtime.Handle, args ...string) *exec.Cmd {
	// A service-private /tmp namespace changes across systemd restarts. Keep the
	// socket with durable run metadata so surviving tmux sessions stay reachable.
	all := []string{"-S", filepath.Join(h.logDir, string(handle), "tmux.sock"), "-f", os.DevNull}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, h.tmuxBinary, all...)
	cmd.Env = runtime.AgentEnvironment(nil)
	return cmd
}
func (h *HostRuntime) paths(handle runtime.Handle) (string, string, string) {
	dir := filepath.Join(h.logDir, string(handle))
	return filepath.Join(dir, "output"), filepath.Join(dir, "exit"), filepath.Join(dir, "supervisor.py")
}
func (h *HostRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	if err := runtime.ValidateSession(spec.SessionName); err != nil {
		return "", err
	}
	if spec.AutoApprove {
		return "", fmt.Errorf("auto approval requires Docker")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("runtime requires python3: %w", err)
	}
	if err := os.MkdirAll(h.logDir, 0700); err != nil {
		return "", err
	}
	handle := runtime.Handle(spec.SessionName)
	log, status, script := h.paths(handle)
	if err := os.Mkdir(filepath.Dir(log), 0700); err != nil {
		return "", fmt.Errorf("reserve runtime attempt: %w", err)
	}
	if err := os.WriteFile(script, runtime.SupervisorScript(), 0600); err != nil {
		return "", err
	}
	argv := spec.Command
	if len(argv) == 0 {
		argv = []string{"sleep", "infinity"}
	}
	args := []string{"new-session", "-d", "-s", spec.SessionName}
	if spec.WorkDir != "" {
		args = append(args, "-c", spec.WorkDir)
	}
	args = append(args, python, script, "run", log, status, spec.SetupCommand)
	args = append(args, argv...)
	cmd := h.command(ctx, handle, args...)
	settings := make(map[string]string, len(spec.Env)+2)
	for k, v := range spec.Env {
		settings[k] = v
	}
	settings["AGENT_DASHBOARD_SUPERVISOR"] = script
	settings["AGENT_DASHBOARD_RUNTIME"] = "host"
	settings["AGENT_DASHBOARD_MODE"] = spec.Mode
	cmd.Env = runtime.AgentEnvironment(settings)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("start tmux: %s: %w", out, err)
	}
	return handle, nil
}
func (h *HostRuntime) Exec(ctx context.Context, handle runtime.Handle, args []string) (io.ReadCloser, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("input requires exactly one literal payload")
	}
	if err := h.command(ctx, handle, "send-keys", "-l", "-t", string(handle), "--", strings.TrimSuffix(args[0], "\n")).Run(); err != nil {
		return nil, err
	}
	if err := h.command(ctx, handle, "send-keys", "-t", string(handle), "Enter").Run(); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (h *HostRuntime) Interrupt(ctx context.Context, handle runtime.Handle) error {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return err
	}
	return h.command(ctx, handle, "send-keys", "-t", string(handle), "C-c").Run()
}
func (h *HostRuntime) Output(ctx context.Context, handle runtime.Handle) (io.ReadCloser, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return nil, err
	}
	log, status, script := h.paths(handle)
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("%w: %s", runtime.ErrNotFound, handle)
	}
	cmd := exec.Command("python3", script, "follow", log, status)
	cmd.Env = runtime.AgentEnvironment(nil)
	return runtime.CommandReader(ctx, cmd)
}

func (h *HostRuntime) Attach(ctx context.Context, handle runtime.Handle) (runtime.PTY, error) {
	cmd := h.command(ctx, handle, "attach-session", "-t", string(handle))
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to attach pty to tmux session %q: %w", handle, err)
	}
	return &ptyWrapper{f: ptmx}, nil
}

func (h *HostRuntime) Stats(ctx context.Context, handle runtime.Handle) (runtime.Usage, error) {
	// Get PID of pane inside tmux
	cmd := h.command(ctx, handle, "list-panes", "-t", string(handle), "-F", "#{pane_pid}")
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
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return err
	}
	out, err := h.command(ctx, handle, "kill-session", "-t", string(handle)).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := string(out)
		if !strings.Contains(message, "no server running") && !strings.Contains(message, "No such file or directory") && !strings.Contains(message, "can't find session") {
			return fmt.Errorf("kill tmux runtime: %s: %w", out, err)
		}
	}
	// Session validation confines cleanup to this run's private directory.
	// Dashboard shutdown never calls Destroy.
	return os.RemoveAll(filepath.Join(h.logDir, string(handle)))
}
func (h *HostRuntime) ListSessions(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(h.logDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := h.Inspect(ctx, runtime.Handle(entry.Name()))
		if err == nil && info.Status == "running" {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
func (h *HostRuntime) Inspect(ctx context.Context, handle runtime.Handle) (runtime.ContainerInfo, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return runtime.ContainerInfo{}, err
	}
	_, status, script := h.paths(handle)
	if data, err := os.ReadFile(status); err == nil {
		code, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return runtime.ContainerInfo{}, fmt.Errorf("invalid recorded runtime exit: %w", err)
		}
		return runtime.ContainerInfo{Status: "exited", ExitCode: code}, nil
	}
	if _, err := os.Stat(script); os.IsNotExist(err) {
		return runtime.ContainerInfo{}, fmt.Errorf("%w: %s", runtime.ErrNotFound, handle)
	}
	if err := h.command(ctx, handle, "has-session", "-t", string(handle)).Run(); err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("runtime exit status unavailable: %w", err)
	}
	return runtime.ContainerInfo{Status: "running"}, nil
}
