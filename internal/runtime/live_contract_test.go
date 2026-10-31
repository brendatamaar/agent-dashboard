package runtime_test

import (
	rt "agent-dashboard/internal/runtime"
	"agent-dashboard/internal/runtime/docker"
	"agent-dashboard/internal/runtime/host"
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveDurableOutputInputAndExit(t *testing.T) {
	if os.Getenv("AGENTD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("requires Linux tmux, matching sandbox image and AGENTD_RUNTIME_INTEGRATION=1")
	}
	logDir := t.TempDir()
	for _, subject := range []struct {
		name   string
		create func() rt.Runtime
	}{{"host", func() rt.Runtime { return host.NewHostRuntimeWithLogDir(logDir) }}, {"docker", func() rt.Runtime { return docker.NewDockerRuntime() }}} {
		t.Run(subject.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			dir := t.TempDir()
			if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
				t.Fatalf("init: %s %v", out, err)
			}
			runtime := subject.create()
			h, err := runtime.Create(ctx, rt.RuntimeSpec{SessionName: fmt.Sprintf("contract-io-%d", time.Now().UnixNano()), Mode: "task", WorkDir: dir, Command: []string{"python3", "-u", "-c", `import sys; print('ready',flush=True); value=sys.stdin.readline(); print('INPUT:'+repr(value),flush=True); sys.exit(7)`}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = runtime.Destroy(context.Background(), h) }()
			output, err := runtime.Output(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.Close() }()
			reader := bufio.NewReader(output)
			ready, err := reader.ReadString('\n')
			if err != nil || ready != "ready\n" {
				t.Fatalf("initial output: %q %v", ready, err)
			}
			literal := "C-c ; $(touch should-never-run)  exact text"
			ack, err := runtime.Exec(ctx, h, []string{literal})
			if err != nil {
				t.Fatal(err)
			}
			_ = ack.Close()
			rest, err := io.ReadAll(reader)
			if err != nil || !strings.Contains(string(rest), literal) {
				t.Fatalf("literal input fidelity lost: %q %v", rest, err)
			}
			var info rt.ContainerInfo
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				info, err = runtime.Inspect(ctx, h)
				if err != nil {
					t.Fatal(err)
				}
				if info.Status == "exited" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if info.Status != "exited" || info.ExitCode != 7 {
				t.Fatalf("real exit lost: %+v", info)
			}
			// New adapter instance models dashboard restart; durable source replays fully.
			recovered := subject.create()
			replay, err := recovered.Output(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(replay)
			_ = replay.Close()
			if err != nil || string(data) != "ready\n"+string(rest) {
				t.Fatalf("restart replay changed: %q %v", data, err)
			}
		})
	}
}
