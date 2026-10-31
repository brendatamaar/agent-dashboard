package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness/claude"
)

func TestClaudeAdapterCommands(t *testing.T) {
	adapter := claude.NewAdapter()
	if adapter.Name() != "claude" {
		t.Fatalf("expected adapter name claude, got %s", adapter.Name())
	}

	// Task mode command
	specTask := agent.AgentSpec{
		Mode:   "task",
		Model:  "claude-sonnet-5-5",
		Prompt: "write tests",
	}
	startCmd := adapter.StartCommand(specTask)
	expectedStart := []string{"claude", "-p", "--output-format=stream-json", "--verbose", "--model", "claude-sonnet-5-5", "write tests"}
	if len(startCmd) != len(expectedStart)+2 {
		t.Fatalf("expected len %d, got %d (%v)", len(expectedStart), len(startCmd), startCmd)
	}
	for i, arg := range expectedStart {
		if startCmd[i] != arg {
			t.Errorf("arg %d: expected %q, got %q", i, arg, startCmd[i])
		}
	}

	// Resume task command
	resumeCmd := adapter.ResumeCommand(specTask, "session-xyz")
	expectedResume := []string{"claude", "-p", "--output-format=stream-json", "--verbose", "--resume", "session-xyz", "--model", "claude-sonnet-5-5", "write tests"}
	if len(resumeCmd) != len(expectedResume)+2 {
		t.Fatalf("expected resume len %d, got %d (%v)", len(expectedResume), len(resumeCmd), resumeCmd)
	}

	// Interactive mode command
	specInteractive := agent.AgentSpec{
		Mode: "interactive",
	}
	interactiveCmd := adapter.StartCommand(specInteractive)
	if len(interactiveCmd) != 3 || interactiveCmd[0] != "claude" || interactiveCmd[1] != "--settings" {
		t.Fatalf("unexpected interactive command: %v", interactiveCmd)
	}

	// Respond
	if string(adapter.Respond(true)) != "y\n" {
		t.Fatalf("expected y\\n for approve")
	}
	if string(adapter.Respond(false)) != "n\n" {
		t.Fatalf("expected n\\n for deny")
	}
}

func TestClaudeParseEventsFixture(t *testing.T) {
	adapter := claude.NewAdapter()
	fixturePath := filepath.Join("testdata", "claude_stream.ndjson")
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

	// Expecting:
	// 1. system init (message event)
	// 2. assistant message: "I am analyzing your codebase."
	// 3. tool_use: Bash "git status"
	// 4. tool_result: "On branch main\nnothing to commit"
	// 5. assistant message: "The git workspace is clean and ready."
	// 6. usage: 120 tokens in, 45 out, 0.005 USD
	// 7. done: "Done analyzing repo.", completed
	if len(parsed) != 7 {
		t.Fatalf("expected 7 parsed events, got %d", len(parsed))
	}

	if parsed[0].Type != events.EventMessage || parsed[0].Payload["session_id"] != "test-session-123" {
		t.Errorf("event 0 unexpected: %+v", parsed[0])
	}
	if parsed[1].Type != events.EventMessage || parsed[1].Payload["text"] != "I am analyzing your codebase." {
		t.Errorf("event 1 unexpected: %+v", parsed[1])
	}
	if parsed[2].Type != events.EventToolCall || parsed[2].Payload["name"] != "Bash" {
		t.Errorf("event 2 unexpected: %+v", parsed[2])
	}
	if parsed[3].Type != events.EventToolResult || parsed[3].Payload["id"] != "call_abc" {
		t.Errorf("event 3 unexpected: %+v", parsed[3])
	}
	if parsed[4].Type != events.EventMessage || parsed[4].Payload["text"] != "The git workspace is clean and ready." {
		t.Errorf("event 4 unexpected: %+v", parsed[4])
	}
	if parsed[5].Type != events.EventUsage || parsed[5].Payload["input_tokens"] != 120 {
		t.Errorf("event 5 unexpected: %+v", parsed[5])
	}
	if parsed[6].Type != events.EventDone || parsed[6].Payload["result"] != "Done analyzing repo." {
		t.Errorf("event 6 unexpected: %+v", parsed[6])
	}
}
