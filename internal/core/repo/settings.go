package repo

import (
	"fmt"
	"strings"
)

type RepoSettings struct {
	Runtime      string            `json:"runtime"`       // "docker" (default) | "host"
	Image        string            `json:"image"`         // container image, defaults to "agentd/sandbox:latest"
	SetupCommand string            `json:"setup_command"` // optional setup cmd e.g. "npm ci"
	EnvVars      map[string]string `json:"env_vars"`      // non-secret environment variables
	AllowedTools []string          `json:"allowed_tools"` // pre-approved tool names
	AutoApprove  bool              `json:"auto_approve"`  // only allowed if runtime == "docker"
}

func DefaultRepoSettings() RepoSettings {
	return RepoSettings{
		Runtime:      "docker",
		Image:        "agentd/sandbox:latest",
		SetupCommand: "",
		EnvVars:      make(map[string]string),
		AllowedTools: make([]string, 0),
		AutoApprove:  false,
	}
}

func ValidateRepoSettings(s RepoSettings) error {
	rt := strings.ToLower(strings.TrimSpace(s.Runtime))
	if rt == "" {
		rt = "docker"
	}
	if rt != "docker" && rt != "host" {
		return fmt.Errorf("invalid runtime %q: must be 'docker' or 'host'", s.Runtime)
	}
	if s.AutoApprove && rt != "docker" {
		return fmt.Errorf("auto-approve is only permitted with the 'docker' runtime")
	}
	for k := range s.EnvVars {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("environment variable key cannot be empty")
		}
	}
	return nil
}
