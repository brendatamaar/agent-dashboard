package harness_test

import (
	"testing"

	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/harness/codex"
	"agent-dashboard/internal/harness/gemini"
	"agent-dashboard/internal/harness/muse"
)

func TestAllHarnessesContract(t *testing.T) {
	harnesses := []harness.Harness{
		claude.NewAdapter(),
		codex.NewAdapter(),
		gemini.NewAdapter(),
		muse.NewAdapter(),
	}

	for _, h := range harnesses {
		t.Run(h.Name(), func(t *testing.T) {
			RunHarnessContractTests(t, h)
		})
	}
}

func TestHarnessRegistry(t *testing.T) {
	reg := harness.NewRegistry()
	reg.Register(claude.NewAdapter())
	reg.Register(codex.NewAdapter())
	reg.Register(gemini.NewAdapter())
	reg.Register(muse.NewAdapter())

	if len(reg.All()) != 4 {
		t.Fatalf("expected 4 harnesses in registry, got %d", len(reg.All()))
	}

	for _, name := range []string{"claude", "codex", "gemini", "muse"} {
		h, ok := reg.Get(name)
		if !ok || h.Name() != name {
			t.Fatalf("expected to find %s in registry", name)
		}
	}
}
