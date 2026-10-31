package runtime

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("runtime session not found")
var ErrUnsupported = errors.New("runtime requires Linux")

//go:embed supervisor.py
var supervisorFS embed.FS

func SupervisorScript() []byte { b, _ := supervisorFS.ReadFile("supervisor.py"); return b }

type Handle string

type RuntimeSpec struct {
	Mode          string
	SetupCommand  string
	SessionName   string
	WorkDir       string
	Command       []string
	Env           map[string]string
	Image         string
	MemoryBytes   int64
	CPUs          float64
	AutoApprove   bool
	WorktreeMount string
}

type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

type Usage struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

type ContainerInfo struct {
	Status    string `json:"status"` // "running", "stopped", "exited"
	ExitCode  int    `json:"exit_code"`
	OOMKilled bool   `json:"oom_killed"`
}

// Runtime separates literal input from durable replayable output. Unknown
// inspection outcomes are errors, never a successful zero exit. Destroy is idempotent.
type Runtime interface {
	Name() string
	Create(ctx context.Context, spec RuntimeSpec) (Handle, error)
	Exec(ctx context.Context, h Handle, cmd []string) (io.ReadCloser, error)
	Output(ctx context.Context, h Handle) (io.ReadCloser, error)
	Interrupt(ctx context.Context, h Handle) error
	Attach(ctx context.Context, h Handle) (PTY, error)
	Stats(ctx context.Context, h Handle) (Usage, error)
	Destroy(ctx context.Context, h Handle) error
	ListSessions(ctx context.Context) ([]string, error)
	Inspect(ctx context.Context, h Handle) (ContainerInfo, error)
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
	outputs  map[string]string
	inputs   map[string][]string
	infos    map[string]ContainerInfo
}

func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{
		sessions: make(map[string]RuntimeSpec),
		outputs:  make(map[string]string), inputs: make(map[string][]string), infos: make(map[string]ContainerInfo),
	}
}

func (f *FakeRuntime) Name() string {
	return "fake"
}

func (f *FakeRuntime) Create(ctx context.Context, spec RuntimeSpec) (Handle, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := ValidateSession(spec.SessionName); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.sessions[spec.SessionName]; exists {
		return "", fmt.Errorf("session %q already exists", spec.SessionName)
	}
	f.sessions[spec.SessionName] = spec
	return Handle(spec.SessionName), nil
}

func (f *FakeRuntime) Exec(ctx context.Context, h Handle, cmd []string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(cmd) != 1 {
		return nil, fmt.Errorf("input requires exactly one literal payload")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	f.inputs[string(h)] = append(f.inputs[string(h)], cmd[0])
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *FakeRuntime) Attach(ctx context.Context, h Handle) (PTY, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	return &FakePTY{Rows: 24, Cols: 80}, nil
}

func (f *FakeRuntime) Stats(ctx context.Context, h Handle) (Usage, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return Usage{}, fmt.Errorf("%w: %s", ErrNotFound, h)
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

func (f *FakeRuntime) Inspect(ctx context.Context, h Handle) (ContainerInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, exists := f.sessions[string(h)]; !exists {
		return ContainerInfo{}, fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	if info, ok := f.infos[string(h)]; ok {
		return info, nil
	}
	return ContainerInfo{Status: "running", ExitCode: 0, OOMKilled: false}, nil
}

func (f *FakeRuntime) Output(ctx context.Context, h Handle) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.sessions[string(h)]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	return io.NopCloser(strings.NewReader(f.outputs[string(h)])), nil
}
func (f *FakeRuntime) Interrupt(ctx context.Context, h Handle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.sessions[string(h)]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	return nil
}

// SeedOutput and Complete are simulator controls, never production operations.
func (f *FakeRuntime) SeedOutput(h Handle, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs[string(h)] = data
}
func (f *FakeRuntime) Complete(h Handle, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infos[string(h)] = ContainerInfo{Status: "exited", ExitCode: code}
}
func (f *FakeRuntime) Inputs(h Handle) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]string(nil), f.inputs[string(h)]...)
}
