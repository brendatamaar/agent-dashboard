//go:build !windows

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/creack/pty"

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
	return pty.Setsize(p.f, &pty.Winsize{Rows: rows, Cols: cols})
}

func (d *DockerRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	if err := runtime.ValidateSession(spec.SessionName); err != nil {
		return "", err
	}

	containerName := spec.SessionName
	image := spec.Image
	if image == "" {
		image = d.defaultImage
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 || gid <= 0 {
		return "", fmt.Errorf("Docker runtime must run as the non-root agentd service account")
	}
	identity := fmt.Sprintf("%d:%d", uid, gid)
	imageIdentity, err := exec.CommandContext(ctx, d.dockerBinary, "image", "inspect", "--format", `{{ index .Config.Labels "agent-dashboard.uid" }}:{{ index .Config.Labels "agent-dashboard.gid" }}`, image).Output()
	if err != nil {
		return "", fmt.Errorf("inspect sandbox image; build scripts/docker with AGENTD_UID=%d and AGENTD_GID=%d: %w", uid, gid, err)
	}
	if strings.TrimSpace(string(imageIdentity)) != identity {
		return "", fmt.Errorf("sandbox image identity does not match service user; rebuild image with --build-arg AGENTD_UID=%d --build-arg AGENTD_GID=%d", uid, gid)
	}

	memBytes := spec.MemoryBytes
	if memBytes <= 0 {
		memBytes = DefaultMemoryBytes
	}
	memMB := memBytes / (1024 * 1024)
	swapMB := memMB + 500 // 1200m total swap limit

	cpus := spec.CPUs
	if cpus <= 0 {
		cpus = DefaultCPUs
	}
	args := []string{
		"run", "-d",
		"--name", containerName,
		fmt.Sprintf("--memory=%dm", memMB),
		fmt.Sprintf("--memory-swap=%dm", swapMB),
		fmt.Sprintf("--cpus=%.2f", cpus),
		"--label=agent-dashboard.managed=true",
		"--log-driver=json-file",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=" + identity,
	}

	// Mount only the agent worktree read-write
	mountSrc := spec.WorktreeMount
	if mountSrc == "" {
		mountSrc = spec.WorkDir
	}
	if mountSrc != "" {
		resolved, err := filepath.Abs(mountSrc)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(resolved, ".git", "HEAD")); err != nil {
			return "", fmt.Errorf("isolated worktree metadata is required: %w", err)
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,src=%s,dst=/workspace", resolved))
	}
	args = append(args, "-w", "/workspace")

	// Inject secrets and env vars (never printed or saved to disk)
	for k, v := range spec.Env {
		if strings.HasPrefix(k, "AGENTD_") || strings.ContainsAny(k, "=\x00") {
			continue
		}
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, "-e", "AGENT_DASHBOARD_SUPERVISOR=/usr/local/lib/agentd-supervisor.py", "-e", "AGENT_DASHBOARD_RUNTIME=docker", "-e", "AGENT_DASHBOARD_MODE="+spec.Mode)
	args = append(args, image)

	// Container command: start tmux session inside container
	cmdToRun := spec.Command
	if len(cmdToRun) == 0 {
		cmdToRun = []string{"sleep", "infinity"}
	}
	args = append(args, "python3", "/usr/local/lib/agentd-supervisor.py", "container", "/tmp/agentd/output", "/tmp/agentd/exit", spec.SessionName, spec.SetupCommand)
	args = append(args, cmdToRun...)

	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker run failed: %s: %w", stderr.String(), err)
	}

	return runtime.Handle(containerName), nil
}

func (d *DockerRuntime) Exec(ctx context.Context, handle runtime.Handle, args []string) (io.ReadCloser, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("input requires exactly one literal payload")
	}
	if err := exec.CommandContext(ctx, d.dockerBinary, "exec", string(handle), "tmux", "-L", "agentd", "send-keys", "-l", "-t", string(handle), "--", strings.TrimSuffix(args[0], "\n")).Run(); err != nil {
		return nil, err
	}
	if err := exec.CommandContext(ctx, d.dockerBinary, "exec", string(handle), "tmux", "-L", "agentd", "send-keys", "-t", string(handle), "Enter").Run(); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (d *DockerRuntime) Interrupt(ctx context.Context, handle runtime.Handle) error {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return err
	}
	return exec.CommandContext(ctx, d.dockerBinary, "exec", string(handle), "tmux", "-L", "agentd", "send-keys", "-t", string(handle), "C-c").Run()
}
func (d *DockerRuntime) Output(ctx context.Context, handle runtime.Handle) (io.ReadCloser, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return nil, err
	}
	if _, err := d.Inspect(ctx, handle); err != nil {
		return nil, err
	}
	return runtime.CommandReader(ctx, exec.Command(d.dockerBinary, "logs", "--follow", string(handle)))
}

func (d *DockerRuntime) Attach(ctx context.Context, handle runtime.Handle) (runtime.PTY, error) {
	args := []string{"exec", "-it", string(handle), "tmux", "-L", "agentd", "attach-session", "-t", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to attach PTY to container %q: %w", handle, err)
	}
	return &ptyWrapper{f: ptmx}, nil
}

func (d *DockerRuntime) Stats(ctx context.Context, handle runtime.Handle) (runtime.Usage, error) {
	args := []string{"stats", "--no-stream", "--format", "{{.CPUPerc}}\t{{.MemUsage}}", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		return runtime.Usage{}, fmt.Errorf("failed to get docker stats for %q: %w", handle, err)
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	var cpuPercent float64
	var memBytes uint64

	if len(parts) >= 1 {
		cpuStr := strings.TrimSuffix(parts[0], "%")
		if v, err := strconv.ParseFloat(cpuStr, 64); err == nil {
			cpuPercent = v
		}
	}
	if len(parts) >= 2 {
		memStr := parts[1]
		// Format: 12.5MiB / 700MiB
		sub := strings.Split(memStr, "/")
		if len(sub) > 0 {
			usedStr := strings.TrimSpace(sub[0])
			if strings.HasSuffix(usedStr, "MiB") {
				if v, err := strconv.ParseFloat(strings.TrimSuffix(usedStr, "MiB"), 64); err == nil {
					memBytes = uint64(v * 1024 * 1024)
				}
			} else if strings.HasSuffix(usedStr, "GiB") {
				if v, err := strconv.ParseFloat(strings.TrimSuffix(usedStr, "GiB"), 64); err == nil {
					memBytes = uint64(v * 1024 * 1024 * 1024)
				}
			}
		}
	}

	return runtime.Usage{
		CPUPercent:  cpuPercent,
		MemoryBytes: memBytes,
	}, nil
}

func (d *DockerRuntime) Destroy(ctx context.Context, handle runtime.Handle) error {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return err
	}
	args := []string{"rm", "-f", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "No such") {
		return fmt.Errorf("remove Docker runtime: %w", err)
	}
	return nil
}

func (d *DockerRuntime) ListSessions(ctx context.Context) ([]string, error) {
	args := []string{"ps", "--filter", "label=agent-dashboard.managed=true", "--format", "{{.Names}}"}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list Docker sessions: %w", err)
	}

	var list []string
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			list = append(list, l)
		}
	}
	return list, nil
}

type dockerInspectState struct {
	Status    string `json:"Status"`
	ExitCode  int    `json:"ExitCode"`
	OOMKilled bool   `json:"OOMKilled"`
}

type dockerInspectResult struct {
	State dockerInspectState `json:"State"`
}

func (d *DockerRuntime) Inspect(ctx context.Context, handle runtime.Handle) (runtime.ContainerInfo, error) {
	if err := runtime.ValidateSession(string(handle)); err != nil {
		return runtime.ContainerInfo{}, err
	}
	args := []string{"inspect", "--type=container", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && strings.Contains(string(ee.Stderr), "No such") {
			return runtime.ContainerInfo{}, fmt.Errorf("%w: %s", runtime.ErrNotFound, handle)
		}
		return runtime.ContainerInfo{}, fmt.Errorf("inspect Docker session: %w", err)
	}

	var results []dockerInspectResult
	if err := json.Unmarshal(out, &results); err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("decode Docker inspect: %w", err)
	}
	if len(results) != 1 {
		return runtime.ContainerInfo{}, fmt.Errorf("invalid Docker inspect result")
	}

	st := results[0].State
	return runtime.ContainerInfo{
		Status:    st.Status,
		ExitCode:  st.ExitCode,
		OOMKilled: st.OOMKilled,
	}, nil
}
