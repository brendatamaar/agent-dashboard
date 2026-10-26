package host

import (
	"agent-dashboard/internal/runtime"
)

type HostRuntime struct {
	// Options can include custom tmux binary path if needed
	tmuxBinary string
}

func NewHostRuntime() *HostRuntime {
	return &HostRuntime{
		tmuxBinary: "tmux",
	}
}

func (h *HostRuntime) Name() string {
	return "host"
}

// Compile-time check that HostRuntime implements runtime.Runtime
var _ runtime.Runtime = (*HostRuntime)(nil)
