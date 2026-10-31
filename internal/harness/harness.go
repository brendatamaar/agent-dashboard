package harness

import (
	"context"
	"fmt"
	"io"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
)

type VersionInfo struct {
	Version string `json:"version"`
	Raw     string `json:"raw"`
}

type AuthStatus struct {
	LoggedIn   bool   `json:"logged_in"`
	AuthMethod string `json:"auth_method"`
	Email      string `json:"email"`
}

type Harness interface {
	Name() string
	Check(ctx context.Context) (VersionInfo, AuthStatus, error)
	StartCommand(spec agent.AgentSpec) []string
	ResumeCommand(spec agent.AgentSpec, sessionID string) []string
	ParseEvents(ctx context.Context, r io.Reader) (<-chan events.NormalizedEvent, <-chan error)
	Respond(approved bool) []byte
}

// SendEvent unblocks backpressure as soon as the parser is cancelled.
func SendEvent(ctx context.Context, out chan<- events.NormalizedEvent, event events.NormalizedEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// CloseOnCancel lets cancellation interrupt an underlying runtime pipe read.
// Non-closable readers must be finite; live sources implement io.ReadCloser.
func CloseOnCancel(ctx context.Context, r io.Reader) func() {
	closer, ok := r.(io.Closer)
	if !ok {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// ValidateSpec rejects settings that an adapter cannot faithfully implement.
func ValidateSpec(h Harness, spec agent.AgentSpec) error {
	if len(spec.AllowedTools) > 0 && h.Name() != "claude" {
		return fmt.Errorf("harness %s does not support the configured allowed tools list", h.Name())
	}
	return nil
}
