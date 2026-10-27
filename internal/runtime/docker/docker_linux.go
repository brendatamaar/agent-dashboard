//go:build !windows

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/creack/pty"

	"agent-dashboard/internal/runtime"
)

type ptyWrapper struct {
	f io.ReadWriteCloser
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
	if pf, ok := p.f.(interface{ Resize(uint16, uint16) error }); ok {
		return pf.Resize(rows, cols)
	}
	return nil
}

func (d *DockerRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	if spec.SessionName == "" {
		return "", fmt.Errorf("session name is required")
	}

	containerName := spec.SessionName
	image := spec.Image
	if image == "" {
		image = d.defaultImage
	}

	memBytes := spec.MemoryBytes
	if memBytes <= 0 {
		memBytes = DefaultMemoryBytes
	}
	memMB := memBytes / (1024 * 1024)
	swapMB := memMB + 500 // 1200m total swap limit

	args := []string{
		"run", "-d",
		"--name", containerName,
		fmt.Sprintf("--memory=%dm", memMB),
		fmt.Sprintf("--memory-swap=%dm", swapMB),
		fmt.Sprintf("--cpus=%.1f", DefaultCPUs),
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=1001:1001",
	}

	// Mount only the agent worktree read-write
	mountSrc := spec.WorktreeMount
	if mountSrc == "" {
		mountSrc = spec.WorkDir
	}
	if mountSrc != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/workspace:rw", mountSrc))
	}
	args = append(args, "-w", "/workspace")

	// Inject secrets and env vars (never printed or saved to disk)
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, image)

	// Container command: start tmux session inside container
	cmdToRun := spec.Command
	if len(cmdToRun) == 0 {
		cmdToRun = []string{"sleep", "infinity"}
	}
	args = append(args, "tmux", "new-session", "-d", "-s", spec.SessionName)
	args = append(args, cmdToRun...)

	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker run failed: %s: %w", stderr.String(), err)
	}

	return runtime.Handle(containerName), nil
}

func (d *DockerRuntime) Exec(ctx context.Context, handle runtime.Handle, cmdArgs []string) (io.ReadCloser, error) {
	joined := strings.Join(cmdArgs, " ")
	args := []string{"exec", string(handle), "tmux", "send-keys", "-t", string(handle), joined, "Enter"}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker exec send-keys failed: %w", err)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (d *DockerRuntime) Attach(ctx context.Context, handle runtime.Handle) (runtime.PTY, error) {
	args := []string{"exec", "-it", string(handle), "tmux", "attach-session", "-t", string(handle)}
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
	args := []string{"rm", "-f", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	_ = cmd.Run()
	return nil
}

func (d *DockerRuntime) ListSessions(ctx context.Context) ([]string, error) {
	args := []string{"ps", "--format", "{{.Names}}"}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
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
	args := []string{"inspect", string(handle)}
	cmd := exec.CommandContext(ctx, d.dockerBinary, args...)
	out, err := cmd.Output()
	if err != nil {
		return runtime.ContainerInfo{Status: "stopped", ExitCode: 0, OOMKilled: false}, nil
	}

	var results []dockerInspectResult
	if err := json.Unmarshal(out, &results); err != nil || len(results) == 0 {
		return runtime.ContainerInfo{Status: "stopped", ExitCode: 0, OOMKilled: false}, nil
	}

	st := results[0].State
	return runtime.ContainerInfo{
		Status:    st.Status,
		ExitCode:  st.ExitCode,
		OOMKilled: st.OOMKilled,
	}, nil
}
