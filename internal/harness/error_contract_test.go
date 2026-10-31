package harness_test

import (
	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/antigravity"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/harness/codex"
	"agent-dashboard/internal/harness/gemini"
	"agent-dashboard/internal/harness/muse"
	"context"
	"strings"
	"testing"
	"time"
)

func TestParserErrorChannelNeverBlocksFallback(t *testing.T) {
	input := "invalid json\n" + strings.Repeat("x", 10*1024*1024+1)
	for _, h := range []harness.Harness{claude.NewAdapter(), codex.NewAdapter(), gemini.NewAdapter(), muse.NewAdapter(), antigravity.NewAdapter()} {
		t.Run(h.Name(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events, errs := h.ParseEvents(ctx, strings.NewReader(input))
			done := make(chan struct{})
			go func() {
				for range events {
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("error buffer blocked parser fallback")
			}
			if err := <-errs; err == nil {
				t.Fatal("missing fallback warning")
			}
		})
	}
}
