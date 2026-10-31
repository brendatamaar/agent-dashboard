package codex_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness/codex"
)

func TestCodexAdapterCommands(t *testing.T) {
	adapter := codex.NewAdapter()
	if adapter.Name() != "codex" {
		t.Fatalf("expected codex, got %s", adapter.Name())
	}

	specTask := agent.AgentSpec{
		Mode:        "task",
		Model:       "gpt-5.5",
		Prompt:      "refactor auth",
		AutoApprove: true,
	}
	startCmd := adapter.StartCommand(specTask)
	expectedStart := []string{"codex", "exec", "--json", "--model", "gpt-5.5", "--dangerously-bypass-approvals-and-sandbox", "refactor auth"}
	if len(startCmd) != len(expectedStart) {
		t.Fatalf("expected len %d, got %d (%v)", len(expectedStart), len(startCmd), startCmd)
	}
	for i, arg := range expectedStart {
		if startCmd[i] != arg {
			t.Errorf("arg %d: expected %q, got %q", i, arg, startCmd[i])
		}
	}

	resumeCmd := adapter.ResumeCommand(specTask, "sess-456")
	expectedResume := []string{"codex", "exec", "resume", "sess-456", "refactor auth", "--json", "--model", "gpt-5.5", "--dangerously-bypass-approvals-and-sandbox"}
	if len(resumeCmd) != len(expectedResume) {
		t.Fatalf("expected resume len %d, got %d (%v)", len(expectedResume), len(resumeCmd), resumeCmd)
	}

	// Interactive
	specInteractive := agent.AgentSpec{
		Mode:  "interactive",
		Model: "gpt-5.5",
	}
	interactiveCmd := adapter.StartCommand(specInteractive)
	if len(interactiveCmd) < 1 || interactiveCmd[0] != "codex" {
		t.Fatalf("unexpected interactive command: %v", interactiveCmd)
	}

	if string(adapter.Respond(true)) != "y\n" {
		t.Fatalf("expected y\\n for approve")
	}
}

func TestCodexParseEventsFixture(t *testing.T) {
	adapter := codex.NewAdapter()
	fixturePath := filepath.Join("testdata", "codex_stream.jsonl")
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("failed to open fixture file: %v", err)
	}
	defer func() { _ = f.Close() }()

	ctx := context.Background()
	evCh, errCh := adapter.ParseEvents(ctx, f)

	var parsed []events.NormalizedEvent
	for ev := range evCh {
		parsed = append(parsed, ev)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("parse events returned error: %v", err)
	}

	if len(parsed) != 8 {
		t.Fatalf("expected 8 parsed events, got %d", len(parsed))
	}

	// 1. thread_started -> system message with thread_id
	if parsed[0].Type != events.EventMessage || parsed[0].Payload["session_id"] != "codex-thread-101" {
		t.Errorf("event 0 unexpected: %+v", parsed[0])
	}
	// 2. assistant message
	if parsed[1].Type != events.EventMessage || parsed[1].Payload["text"] != "Starting review of PR changes." {
		t.Errorf("event 1 unexpected: %+v", parsed[1])
	}
	// 3. tool call
	if parsed[2].Type != events.EventToolCall || parsed[2].Payload["name"] != "shell" {
		t.Errorf("event 2 unexpected: %+v", parsed[2])
	}
	// 4. tool result
	if parsed[3].Type != events.EventToolResult || parsed[3].Payload["content"] != "clean working tree" {
		t.Errorf("event 3 unexpected: %+v", parsed[3])
	}
	// 5. subagent started
	if parsed[4].Type != events.EventSubagentStarted || parsed[4].Payload["subagent_id"] != "sub-codex-1" {
		t.Errorf("event 4 unexpected: %+v", parsed[4])
	}
	// 6. subagent finished
	if parsed[5].Type != events.EventSubagentFinished || parsed[5].Payload["success"] != true {
		t.Errorf("event 5 unexpected: %+v", parsed[5])
	}
	// 7. usage
	if parsed[6].Type != events.EventUsage || parsed[6].Payload["input_tokens"] != 350 {
		t.Errorf("event 6 unexpected: %+v", parsed[6])
	}
	// 8. done
	if parsed[7].Type != events.EventDone || parsed[7].Payload["result"] != "Analysis completed successfully" {
		t.Errorf("event 7 unexpected: %+v", parsed[7])
	}
}
