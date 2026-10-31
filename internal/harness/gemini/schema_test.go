package gemini_test

import (
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness/gemini"
	"context"
	"strings"
	"testing"
)

// Synthetic schema fixture from the official stream types, not a provider run:
// github.com/google-gemini/gemini-cli/packages/core/src/output/types.ts.
func TestOfficialStreamSchema(t *testing.T) {
	stream := `{"type":"message","role":"assistant","content":"hello"}
{"type":"tool_use","tool_id":"one","tool_name":"shell","parameters":{"command":"pwd"}}
{"type":"tool_result","tool_id":"one","status":"error","error":{"type":"error","message":"failed"}}
{"type":"result","status":"error","stats":{"input_tokens":5,"output_tokens":2}}
`
	ch, errs := gemini.NewAdapter().ParseEvents(context.Background(), strings.NewReader(stream))
	var got []events.NormalizedEvent
	for e := range ch {
		got = append(got, e)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 5 || got[0].Payload["text"] != "hello" || got[1].Type != events.EventToolCall || got[2].Payload["is_error"] != true || got[3].Type != events.EventUsage || got[4].Type != events.EventError {
		t.Fatalf("official stream not normalized: %+v", got)
	}
}
