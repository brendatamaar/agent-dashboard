package repo

import (
	"fmt"
	"regexp"
	"strings"
)

var envNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
		if !envNameRegex.MatchString(k) {
			return fmt.Errorf("invalid environment variable key %q", k)
		}
		upper := strings.ToUpper(k)
		if strings.HasPrefix(upper, "AGENT_DASHBOARD_") || strings.HasPrefix(upper, "GIT_") || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
			return fmt.Errorf("reserved environment variable %q", k)
		}
		switch upper {
		case "CLAUDE_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "MUSE_API_KEY", "MUSE_TOKEN", "AGY_TOKEN", "ANTIGRAVITY_TOKEN", "ANTIGRAVITY_API_KEY", "BASH_ENV", "ENV", "PYTHONPATH", "PYTHONHOME", "NODE_OPTIONS":
			return fmt.Errorf("reserved environment variable %q; use encrypted credentials", k)
		}
		if strings.ContainsRune(s.EnvVars[k], 0) {
			return fmt.Errorf("environment variable %q contains NUL", k)
		}
	}
	for _, tool := range s.AllowedTools {
		if strings.TrimSpace(tool) == "" || strings.ContainsAny(tool, "\x00\r\n") {
			return fmt.Errorf("invalid allowed tool name")
		}
	}
	if strings.ContainsRune(s.SetupCommand, 0) {
		return fmt.Errorf("setup command contains NUL")
	}
	return nil
}
