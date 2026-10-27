package antigravity_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness/antigravity"
)

func TestAntigravityAdapterCommands(t *testing.T) {
	adapter := antigravity.NewAdapter()
	if adapter.Name() != "antigravity" {
		t.Fatalf("expected antigravity, got %s", adapter.Name())
	}

	specTask := agent.AgentSpec{
		Mode:   "task",
		Model:  "gemini-2.5-pro",
		Prompt: "check repo structure",
	}
	startCmd := adapter.StartCommand(specTask)
	expectedStart := []string{"agy", "-p", "check repo structure", "--output-format", "stream-json", "--model", "gemini-2.5-pro"}
	if len(startCmd) != len(expectedStart) {
		t.Fatalf("expected len %d, got %d (%v)", len(expectedStart), len(startCmd), startCmd)
	}
	for i, arg := range expectedStart {
		if startCmd[i] != arg {
			t.Errorf("arg %d: expected %q, got %q", i, arg, startCmd[i])
		}
	}

	resumeCmd := adapter.ResumeCommand(specTask, "agy-sess-1001")
	expectedResume := []string{"agy", "-p", "check repo structure", "--output-format", "stream-json", "--conversation", "agy-sess-1001", "--model", "gemini-2.5-pro"}
	if len(resumeCmd) != len(expectedResume) {
		t.Fatalf("expected resume len %d, got %d (%v)", len(expectedResume), len(resumeCmd), resumeCmd)
	}

	// Interactive
	specInteractive := agent.AgentSpec{
		Mode:  "interactive",
		Model: "gemini-2.5-flash",
	}
	interactiveCmd := adapter.StartCommand(specInteractive)
	if len(interactiveCmd) < 1 || interactiveCmd[0] != "agy" {
		t.Fatalf("unexpected interactive command: %v", interactiveCmd)
	}

	if string(adapter.Respond(true)) != "y\n" {
		t.Fatalf("expected y\\n for approve")
	}
}

func TestAntigravityParseEventsFixture(t *testing.T) {
	adapter := antigravity.NewAdapter()
	fixturePath := filepath.Join("testdata", "antigravity_stream.jsonl")
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("failed to open fixture file: %v", err)
	}
	defer f.Close()

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

	// 1. init
	if parsed[0].Type != events.EventMessage || parsed[0].Payload["session_id"] != "agy-sess-1001" {
		t.Errorf("event 0 unexpected: %+v", parsed[0])
	}
	// 2. content
	if parsed[1].Type != events.EventMessage || parsed[1].Payload["text"] != "I am reviewing the repository with Antigravity." {
		t.Errorf("event 1 unexpected: %+v", parsed[1])
	}
	// 3. tool_call
	if parsed[2].Type != events.EventToolCall || parsed[2].Payload["name"] != "read_file" {
		t.Errorf("event 2 unexpected: %+v", parsed[2])
	}
	// 4. tool_response
	if parsed[3].Type != events.EventToolResult || parsed[3].Payload["content"] != "# Agent Dashboard" {
		t.Errorf("event 3 unexpected: %+v", parsed[3])
	}
	// 5. subagent started
	if parsed[4].Type != events.EventSubagentStarted || parsed[4].Payload["subagent_id"] != "sub-agy-1" {
		t.Errorf("event 4 unexpected: %+v", parsed[4])
	}
	// 6. subagent finished
	if parsed[5].Type != events.EventSubagentFinished || parsed[5].Payload["subagent_id"] != "sub-agy-1" {
		t.Errorf("event 5 unexpected: %+v", parsed[5])
	}
	// 7. usage
	if parsed[6].Type != events.EventUsage || parsed[6].Payload["input_tokens"] != 180 {
		t.Errorf("event 6 unexpected: %+v", parsed[6])
	}
	// 8. finish
	if parsed[7].Type != events.EventDone || parsed[7].Payload["result"] != "Review completed successfully." {
		t.Errorf("event 7 unexpected: %+v", parsed[7])
	}
}
