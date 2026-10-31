package harness_test

import (
	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/antigravity"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/harness/codex"
	"agent-dashboard/internal/harness/gemini"
	"agent-dashboard/internal/harness/muse"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParserCancellationAndFallbackContract(t *testing.T) {
	for _, h := range []harness.Harness{claude.NewAdapter(), codex.NewAdapter(), gemini.NewAdapter(), muse.NewAdapter(), antigravity.NewAdapter()} {
		t.Run(h.Name(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			r, w := io.Pipe()
			defer func() { _ = w.Close() }()
			_, errs := h.ParseEvents(ctx, r)
			cancel()
			done := make(chan struct{})
			go func() {
				for range errs {
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("parser cancellation did not close blocking reader")
			}
			ev, errs := h.ParseEvents(context.Background(), strings.NewReader("not json\n"))
			for range ev {
			}
			if err := <-errs; err == nil {
				t.Fatal("malformed output silently discarded")
			}
			ctx, cancel = context.WithCancel(context.Background())
			_, errs = h.ParseEvents(ctx, strings.NewReader(strings.Repeat("{\"type\":\"error\",\"message\":\"x\"}\n", 1000)))
			time.Sleep(10 * time.Millisecond)
			cancel()
			done = make(chan struct{})
			go func() {
				for range errs {
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("parser cancellation blocked behind slow consumer")
			}
		})
	}
}
func TestClaudeFailureResultIsError(t *testing.T) {
	ev, errs := claude.NewAdapter().ParseEvents(context.Background(), strings.NewReader("{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true}\n"))
	var got []events.NormalizedEvent
	for e := range ev {
		got = append(got, e)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 1 || got[0].Type != events.EventError {
		t.Fatalf("failure result not surfaced: %+v", got)
	}
}
func TestCodexDottedEvents(t *testing.T) {
	data := "{\"type\":\"thread.started\",\"thread_id\":\"one\"}\n{\"type\":\"item.completed\",\"item\":{\"id\":\"m\",\"type\":\"agent_message\",\"text\":\"hello\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}\n"
	ev, errs := codex.NewAdapter().ParseEvents(context.Background(), strings.NewReader(data))
	var got []events.NormalizedEvent
	for e := range ev {
		got = append(got, e)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 4 || got[1].Payload["text"] != "hello" || got[3].Type != events.EventDone {
		t.Fatalf("dotted stream not normalized: %+v", got)
	}
}
func TestUnsupportedAllowedToolsRejected(t *testing.T) {
	if err := harness.ValidateSpec(codex.NewAdapter(), agent.AgentSpec{AllowedTools: []string{"Read"}}); err == nil {
		t.Fatal("unsupported setting silently ignored")
	}
	cmd := claude.NewAdapter().StartCommand(agent.AgentSpec{Mode: "task", AllowedTools: []string{"Read"}, AutoApprove: true})
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--allowedTools Read") || !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Fatalf("Claude settings missing: %v", cmd)
	}
}
