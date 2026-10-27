package harness

import (
	"context"
	"fmt"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	harnesses map[string]Harness
}

func NewRegistry() *Registry {
	return &Registry{
		harnesses: make(map[string]Harness),
	}
}

func (r *Registry) Register(h Harness) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.harnesses[h.Name()] = h
}

func (r *Registry) Get(name string) (Harness, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.harnesses[name]
	return h, ok
}

func (r *Registry) All() []Harness {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Harness, 0, len(r.harnesses))
	for _, h := range r.harnesses {
		list = append(list, h)
	}
	return list
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.harnesses))
	for name := range r.harnesses {
		names = append(names, name)
	}
	return names
}

type HarnessInfo struct {
	Name       string      `json:"name"`
	Installed  bool        `json:"installed"`
	Version    VersionInfo `json:"version"`
	AuthStatus AuthStatus  `json:"auth_status"`
	Error      string      `json:"error,omitempty"`
}

func (r *Registry) CheckAll(ctx context.Context) []HarnessInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []HarnessInfo
	for name, h := range r.harnesses {
		info := HarnessInfo{Name: name}
		ver, auth, err := h.Check(ctx)
		if err != nil {
			info.Installed = false
			info.Error = err.Error()
		} else {
			info.Installed = true
			info.Version = ver
			info.AuthStatus = auth
		}
		results = append(results, info)
	}
	return results
}

func (r *Registry) UpdateHarness(ctx context.Context, name string, executor func(ctx context.Context, cmd []string) (string, error)) (string, error) {
	r.mu.RLock()
	_, exists := r.harnesses[name]
	r.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("unknown harness: %s", name)
	}

	var updateCmd []string
	switch name {
	case "claude":
		updateCmd = []string{"npm", "update", "-g", "@anthropic-ai/claude-code"}
	case "codex":
		updateCmd = []string{"codex", "update"}
	case "gemini":
		updateCmd = []string{"npm", "update", "-g", "@google/gemini-cli"}
	case "muse":
		return "Muse Code self-update is managed via platform installer", nil
	case "antigravity":
		return "Antigravity CLI is managed via https://antigravity.google/cli/install.sh", nil
	default:
		return "", fmt.Errorf("no update command configured for %s", name)
	}

	return executor(ctx, updateCmd)
}
