package claude_test

import (
	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/harness/claude"
	"encoding/json"
	"testing"
)

func TestHookSettingsUseQuotedPrivateHelper(t *testing.T) {
	cmd := claude.NewAdapter().StartCommand(agent.AgentSpec{Mode: "task"})
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(cmd[len(cmd)-1]), &settings); err != nil {
		t.Fatal(err)
	}
	hooks := settings.Hooks["PreToolUse"]
	if len(hooks) != 1 || len(hooks[0].Hooks) != 1 || hooks[0].Hooks[0].Command != `python3 "$AGENT_DASHBOARD_SUPERVISOR" hook` {
		t.Fatalf("helper shell quoting changed: %+v", settings)
	}
}
