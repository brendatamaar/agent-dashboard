package host

import (
	"agent-dashboard/internal/runtime"
	"os"
	"path/filepath"
)

type HostRuntime struct {
	// Options can include custom tmux binary path if needed
	tmuxBinary string
	logDir     string
}

func NewHostRuntime() *HostRuntime {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return NewHostRuntimeWithLogDir(filepath.Join(dir, "agent-dashboard", "runtime"))
}
func NewHostRuntimeWithLogDir(dir string) *HostRuntime {
	return &HostRuntime{tmuxBinary: "tmux", logDir: dir}
}

func (h *HostRuntime) Name() string {
	return "host"
}

// Compile-time check that HostRuntime implements runtime.Runtime
var _ runtime.Runtime = (*HostRuntime)(nil)
