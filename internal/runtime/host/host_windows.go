//go:build windows

package host

import (
	"agent-dashboard/internal/runtime"
	"context"
	"io"
)

func (r *HostRuntime) Create(ctx context.Context, s runtime.RuntimeSpec) (runtime.Handle, error) {
	return "", runtime.ErrUnsupported
}
func (r *HostRuntime) Exec(ctx context.Context, h runtime.Handle, args []string) (io.ReadCloser, error) {
	return nil, runtime.ErrUnsupported
}
func (r *HostRuntime) Output(ctx context.Context, h runtime.Handle) (io.ReadCloser, error) {
	return nil, runtime.ErrUnsupported
}
func (r *HostRuntime) Interrupt(ctx context.Context, h runtime.Handle) error {
	return runtime.ErrUnsupported
}
func (r *HostRuntime) Attach(ctx context.Context, h runtime.Handle) (runtime.PTY, error) {
	return nil, runtime.ErrUnsupported
}
func (r *HostRuntime) Stats(ctx context.Context, h runtime.Handle) (runtime.Usage, error) {
	return runtime.Usage{}, runtime.ErrUnsupported
}
func (r *HostRuntime) Inspect(ctx context.Context, h runtime.Handle) (runtime.ContainerInfo, error) {
	return runtime.ContainerInfo{}, runtime.ErrUnsupported
}
func (r *HostRuntime) Destroy(ctx context.Context, h runtime.Handle) error {
	return runtime.ErrUnsupported
}
func (r *HostRuntime) ListSessions(ctx context.Context) ([]string, error) {
	return nil, runtime.ErrUnsupported
}
