package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

var validSession = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,120}$`)

func ValidateSession(name string) error {
	if !validSession.MatchString(name) {
		return fmt.Errorf("invalid runtime session name")
	}
	return nil
}

// AgentEnvironment includes only process basics and explicitly selected settings.
// A dedicated tmux socket additionally prevents an older server's environment
// (including AGENTD_MASTER_KEY) from leaking into a newly launched agent.
func AgentEnvironment(settings map[string]string) []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR", "SYSTEMROOT", "WINDIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range settings {
		if strings.HasPrefix(k, "AGENTD_") || strings.ContainsAny(k, "=\x00") {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// CommandReader terminates only the reader subprocess; closing it never kills
// the persistent runtime session it observes.
func CommandReader(ctx context.Context, cmd *exec.Cmd) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, cancel := context.WithCancel(ctx)
	reader, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	r := &processReader{ReadCloser: reader, cmd: cmd, cancel: cancel, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-r.done:
		}
	}()
	return r, nil
}

type processReader struct {
	io.ReadCloser
	cmd    *exec.Cmd
	cancel context.CancelFunc
	once   sync.Once
	done   chan struct{}
}

func (r *processReader) Close() error {
	var err error
	r.once.Do(func() {
		r.cancel()
		_ = r.ReadCloser.Close()
		_ = r.cmd.Process.Kill()
		err = r.cmd.Wait()
		close(r.done)
	})
	return err
}
