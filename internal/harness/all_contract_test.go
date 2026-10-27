package harness_test

import (
	"testing"

	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/antigravity"
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
		antigravity.NewAdapter(),
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
	reg.Register(antigravity.NewAdapter())

	if len(reg.All()) != 5 {
		t.Fatalf("expected 5 harnesses in registry, got %d", len(reg.All()))
	}

	for _, name := range []string{"claude", "codex", "gemini", "muse", "antigravity"} {
		h, ok := reg.Get(name)
		if !ok || h.Name() != name {
			t.Fatalf("expected to find %s in registry", name)
		}
	}
}
