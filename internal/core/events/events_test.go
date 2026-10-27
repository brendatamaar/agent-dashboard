package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizedEventHelpers(t *testing.T) {
	now := time.Now().UTC()

	msg := NewMessageEvent(1, now, "assistant", "Hello world")
	if msg.Type != EventMessage {
		t.Fatalf("expected message type, got %s", msg.Type)
	}

	payloadBytes, err := msg.MarshalPayload()
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if p["text"] != "Hello world" || p["role"] != "assistant" {
		t.Fatalf("unexpected payload: %v", p)
	}

	usage := NewUsageEvent(2, now, 100, 50, 0.05, "claude-sonnet-5-5")
	if usage.Type != EventUsage {
		t.Fatalf("expected usage type, got %s", usage.Type)
	}

	tool := NewToolCallEvent(3, now, "call_1", "Bash", map[string]any{"command": "ls"})
	if tool.Type != EventToolCall {
		t.Fatalf("expected tool_call type, got %s", tool.Type)
	}

	done := NewDoneEvent(4, now, "Task finished", "completed")
	if done.Type != EventDone {
		t.Fatalf("expected done type, got %s", done.Type)
	}

	subStart := NewSubagentStartedEvent(5, now, "sub-1", "researcher", "find bugs", "parent-1")
	if subStart.Type != EventSubagentStarted {
		t.Fatalf("expected subagent_started type, got %s", subStart.Type)
	}
	if subStart.Payload["subagent_id"] != "sub-1" || subStart.Payload["parent_agent_id"] != "parent-1" {
		t.Fatalf("unexpected subagent_started payload: %v", subStart.Payload)
	}

	subFinish := NewSubagentFinishedEvent(6, now, "sub-1", "found 2 bugs", true)
	if subFinish.Type != EventSubagentFinished {
		t.Fatalf("expected subagent_finished type, got %s", subFinish.Type)
	}
	if subFinish.Payload["success"] != true {
		t.Fatalf("unexpected subagent_finished payload: %v", subFinish.Payload)
	}
}
