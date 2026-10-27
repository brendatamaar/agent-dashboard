package harness_test

import (
	"context"
	"strings"
	"testing"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/harness"
)

// RunHarnessContractTests executes invariant checks required of all Harness implementations.
func RunHarnessContractTests(t *testing.T, h harness.Harness) {
	t.Helper()

	if strings.TrimSpace(h.Name()) == "" {
		t.Fatalf("harness Name() must not be empty")
	}

	specTask := agent.AgentSpec{
		Mode:   "task",
		Model:  "test-model",
		Prompt: "implement feature",
	}

	startCmd := h.StartCommand(specTask)
	if len(startCmd) == 0 {
		t.Fatalf("[%s] StartCommand(task) returned empty slice", h.Name())
	}
	if startCmd[0] != h.Name() && !strings.Contains(startCmd[0], h.Name()) {
		t.Errorf("[%s] StartCommand should invoke harness binary, got %v", h.Name(), startCmd[0])
	}

	specInteractive := agent.AgentSpec{
		Mode: "interactive",
	}
	interactiveCmd := h.StartCommand(specInteractive)
	if len(interactiveCmd) == 0 {
		t.Fatalf("[%s] StartCommand(interactive) returned empty slice", h.Name())
	}

	resumeCmd := h.ResumeCommand(specTask, "test-sess-123")
	if len(resumeCmd) == 0 {
		t.Fatalf("[%s] ResumeCommand returned empty slice", h.Name())
	}

	approveBytes := h.Respond(true)
	denyBytes := h.Respond(false)
	if len(approveBytes) == 0 || len(denyBytes) == 0 {
		t.Fatalf("[%s] Respond() returned empty bytes", h.Name())
	}

	// ParseEvents on empty reader should terminate cleanly without panic
	ctx := context.Background()
	evCh, errCh := h.ParseEvents(ctx, strings.NewReader(""))
	for range evCh {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("[%s] ParseEvents on empty input returned unexpected error: %v", h.Name(), err)
	}
}
