package harness

import (
	"context"
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
