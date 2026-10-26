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
}
