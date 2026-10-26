package runtime

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type Handle string

type RuntimeSpec struct {
	SessionName string
	WorkDir     string
	Command     []string
	Env         map[string]string
}

type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

type Usage struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

type Runtime interface {
	Name() string
	Create(ctx context.Context, spec RuntimeSpec) (Handle, error)
	Exec(ctx context.Context, h Handle, cmd []string) (io.ReadCloser, error)
	Attach(ctx context.Context, h Handle) (PTY, error)
	Stats(ctx context.Context, h Handle) (Usage, error)
	Destroy(ctx context.Context, h Handle) error
	ListSessions(ctx context.Context) ([]string, error)
}

// FakePTY implements PTY in-memory for testing.
type FakePTY struct {
	Reader io.Reader
	Writer io.Writer
	Closer io.Closer
	Rows   uint16
	Cols   uint16
}

func (p *FakePTY) Read(b []byte) (int, error) {
	if p.Reader == nil {
		return 0, io.EOF
	}
	return p.Reader.Read(b)
}

func (p *FakePTY) Write(b []byte) (int, error) {
	if p.Writer == nil {
		return len(b), nil
	}
	return p.Writer.Write(b)
}

func (p *FakePTY) Close() error {
	if p.Closer != nil {
		return p.Closer.Close()
	}
	return nil
}

func (p *FakePTY) Resize(rows, cols uint16) error {
	p.Rows = rows
	p.Cols = cols
	return nil
}

// FakeRuntime implements Runtime in-memory for unit and contract testing.
type FakeRuntime struct {
	mu       sync.RWMutex
	sessions map[string]RuntimeSpec
}

func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{
		sessions: make(map[string]RuntimeSpec),
	}
}

func (f *FakeRuntime) Name() string {
	return "fake"
}

func (f *FakeRuntime) Create(ctx context.Context, spec RuntimeSpec) (Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.sessions[spec.SessionName]; exists {
		return "", fmt.Errorf("session %q already exists", spec.SessionName)
	}
	f.sessions[spec.SessionName] = spec
	return Handle(spec.SessionName), nil
}

func (f *FakeRuntime) Exec(ctx context.Context, h Handle, cmd []string) (io.ReadCloser, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return nil, fmt.Errorf("session %q not found", h)
	}
	return io.NopCloser(io.LimitReader(nil, 0)), nil
}

func (f *FakeRuntime) Attach(ctx context.Context, h Handle) (PTY, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return nil, fmt.Errorf("session %q not found", h)
	}
	return &FakePTY{Rows: 24, Cols: 80}, nil
}

func (f *FakeRuntime) Stats(ctx context.Context, h Handle) (Usage, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return Usage{}, fmt.Errorf("session %q not found", h)
	}
	return Usage{CPUPercent: 1.5, MemoryBytes: 1024 * 1024 * 64}, nil
}

func (f *FakeRuntime) Destroy(ctx context.Context, h Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, string(h))
	return nil
}

func (f *FakeRuntime) ListSessions(ctx context.Context) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var list []string
	for name := range f.sessions {
		list = append(list, name)
	}
	return list, nil
}
