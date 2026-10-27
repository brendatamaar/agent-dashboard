package docker

import (
	"agent-dashboard/internal/runtime"
)

const (
	DefaultSandboxImage = "agentd/sandbox:latest"
	DefaultMemoryBytes  = 700 * 1024 * 1024 // 700 MB limit
	DefaultCPUs         = 1.0
)

type DockerRuntime struct {
	dockerBinary string
	defaultImage string
}

func NewDockerRuntime() *DockerRuntime {
	return &DockerRuntime{
		dockerBinary: "docker",
		defaultImage: DefaultSandboxImage,
	}
}

func (d *DockerRuntime) Name() string {
	return "docker"
}

// Compile-time interface check
var _ runtime.Runtime = (*DockerRuntime)(nil)
