package events

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	EventMessage          EventType = "message"
	EventToolCall         EventType = "tool_call"
	EventToolResult       EventType = "tool_result"
	EventApprovalNeeded   EventType = "approval_needed"
	EventUsage            EventType = "usage"
	EventDone             EventType = "done"
	EventError            EventType = "error"
	EventSubagentStarted  EventType = "subagent_started"
	EventSubagentFinished EventType = "subagent_finished"
)

type NormalizedEvent struct {
	Seq       int64          `json:"seq"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload"`
}

func (e NormalizedEvent) MarshalPayload() ([]byte, error) {
	if e.Payload == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(e.Payload)
}

func NewMessageEvent(seq int64, t time.Time, role, text string) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventMessage,
		Timestamp: t,
		Payload: map[string]any{
			"role": role,
			"text": text,
		},
	}
}

func NewToolCallEvent(seq int64, t time.Time, toolCallID, name string, input any) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventToolCall,
		Timestamp: t,
		Payload: map[string]any{
			"id":    toolCallID,
			"name":  name,
			"input": input,
		},
	}
}

func NewToolResultEvent(seq int64, t time.Time, toolCallID string, content any, isError bool) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventToolResult,
		Timestamp: t,
		Payload: map[string]any{
			"id":       toolCallID,
			"content":  content,
			"is_error": isError,
		},
	}
}

func NewApprovalNeededEvent(seq int64, t time.Time, approvalID, command string, target string, details any) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventApprovalNeeded,
		Timestamp: t,
		Payload: map[string]any{
			"approval_id": approvalID,
			"command":     command,
			"target":      target,
			"details":     details,
		},
	}
}

func NewUsageEvent(seq int64, t time.Time, inputTokens, outputTokens int, costUSD float64, model string) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventUsage,
		Timestamp: t,
		Payload: map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"cost_usd":      costUSD,
			"model":         model,
		},
	}
}

func NewDoneEvent(seq int64, t time.Time, result, terminalReason string) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventDone,
		Timestamp: t,
		Payload: map[string]any{
			"result":          result,
			"terminal_reason": terminalReason,
		},
	}
}

func NewErrorEvent(seq int64, t time.Time, message string, details any) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventError,
		Timestamp: t,
		Payload: map[string]any{
			"message": message,
			"details": details,
		},
	}
}

func NewSubagentStartedEvent(seq int64, t time.Time, subagentID, subagentType, prompt, parentAgentID string) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventSubagentStarted,
		Timestamp: t,
		Payload: map[string]any{
			"subagent_id":     subagentID,
			"subagent_type":   subagentType,
			"prompt":          prompt,
			"parent_agent_id": parentAgentID,
		},
	}
}

func NewSubagentFinishedEvent(seq int64, t time.Time, subagentID, result string, success bool) NormalizedEvent {
	return NormalizedEvent{
		Seq:       seq,
		Type:      EventSubagentFinished,
		Timestamp: t,
		Payload: map[string]any{
			"subagent_id": subagentID,
			"result":      result,
			"success":     success,
		},
	}
}
