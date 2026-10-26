//go:build windows

package host

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"agent-dashboard/internal/runtime"
)

var (
	winMu       sync.RWMutex
	winSessions = make(map[string]runtime.RuntimeSpec)
)

func (h *HostRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	if spec.SessionName == "" {
		return "", fmt.Errorf("session name is required")
	}
	winMu.Lock()
	defer winMu.Unlock()
	winSessions[spec.SessionName] = spec
	return runtime.Handle(spec.SessionName), nil
}

func (h *HostRuntime) Exec(ctx context.Context, handle runtime.Handle, cmdArgs []string) (io.ReadCloser, error) {
	winMu.RLock()
	defer winMu.RUnlock()
	if _, ok := winSessions[string(handle)]; !ok {
		return nil, fmt.Errorf("session %q not found", handle)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (h *HostRuntime) Attach(ctx context.Context, handle runtime.Handle) (runtime.PTY, error) {
	winMu.RLock()
	defer winMu.RUnlock()
	if _, ok := winSessions[string(handle)]; !ok {
		return nil, fmt.Errorf("session %q not found", handle)
	}
	return &runtime.FakePTY{Rows: 24, Cols: 80}, nil
}

func (h *HostRuntime) Stats(ctx context.Context, handle runtime.Handle) (runtime.Usage, error) {
	winMu.RLock()
	defer winMu.RUnlock()
	if _, ok := winSessions[string(handle)]; !ok {
		return runtime.Usage{}, fmt.Errorf("session %q not found", handle)
	}
	return runtime.Usage{CPUPercent: 0.5, MemoryBytes: 1024 * 1024 * 32}, nil
}

func (h *HostRuntime) Destroy(ctx context.Context, handle runtime.Handle) error {
	winMu.Lock()
	defer winMu.Unlock()
	delete(winSessions, string(handle))
	return nil
}

func (h *HostRuntime) ListSessions(ctx context.Context) ([]string, error) {
	winMu.RLock()
	defer winMu.RUnlock()
	var list []string
	for name := range winSessions {
		list = append(list, name)
	}
	return list, nil
}
