//go:build windows

package docker

import (
	"agent-dashboard/internal/runtime"
	"context"
	"io"
)

func (d *DockerRuntime) Create(ctx context.Context, s runtime.RuntimeSpec) (runtime.Handle, error) {
	return "", runtime.ErrUnsupported
}
func (d *DockerRuntime) Exec(ctx context.Context, h runtime.Handle, args []string) (io.ReadCloser, error) {
	return nil, runtime.ErrUnsupported
}
func (d *DockerRuntime) Output(ctx context.Context, h runtime.Handle) (io.ReadCloser, error) {
	return nil, runtime.ErrUnsupported
}
func (d *DockerRuntime) Interrupt(ctx context.Context, h runtime.Handle) error {
	return runtime.ErrUnsupported
}
func (d *DockerRuntime) Attach(ctx context.Context, h runtime.Handle) (runtime.PTY, error) {
	return nil, runtime.ErrUnsupported
}
func (d *DockerRuntime) Stats(ctx context.Context, h runtime.Handle) (runtime.Usage, error) {
	return runtime.Usage{}, runtime.ErrUnsupported
}
func (d *DockerRuntime) Inspect(ctx context.Context, h runtime.Handle) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnsupported
}
func (d *DockerRuntime) Destroy(ctx context.Context, h runtime.Handle) error {
	return runtime.ErrUnsupported
}
func (d *DockerRuntime) ListSessions(ctx context.Context) ([]string, error) {
	return nil, runtime.ErrUnsupported
}
