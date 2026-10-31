package orchestrator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/shell/orchestrator"
	"agent-dashboard/internal/store"
)

type mockRt struct {
	runtime.Runtime
}

func (m *mockRt) Name() string { return "docker" }

func TestOrchestratorSchedulerIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	gitSvc := git.NewService(filepath.Join(tmpDir, "workspace"))
	orch := orchestrator.NewOrchestrator(st, gitSvc, &mockRt{}, nil, nil, 2)
	defer func() { _ = orch.Shutdown(context.Background()) }()

	// Verify Harness registry has all 4 harnesses
	reg := orch.HarnessRegistry()
	if reg == nil {
		t.Fatalf("expected non-nil harness registry")
	}
	for _, h := range []string{"claude", "codex", "gemini", "muse"} {
		if _, ok := reg.Get(h); !ok {
			t.Errorf("expected harness %s registered in orchestrator", h)
		}
	}

	// Create test repo
	repo := store.Repo{
		ID:        "repo-orch-test",
		Name:      "test-repo",
		RemoteURL: "https://github.com/example/test.git",
		Path:      filepath.Join(tmpDir, "workspace", "test-repo"),
	}
	if err := st.CreateRepo(ctx, &repo); err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}

	// Create template
	tmpl := store.Template{
		ID:      "tmpl-orch-nightly",
		Name:    "Nightly Maintenance",
		Harness: "codex",
		Prompt:  "check dependencies and run tests",
	}
	if err := st.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	// Create schedule
	nextRun := time.Now().UTC().Add(-1 * time.Minute) // due now
	sch := store.Schedule{
		ID:         "sch-orch-1",
		TemplateID: "tmpl-orch-nightly",
		RepoID:     "repo-orch-test",
		Cron:       "0 2 * * *",
		Enabled:    true,
		NextRunAt:  &nextRun,
	}
	if err := st.CreateSchedule(ctx, sch); err != nil {
		t.Fatalf("failed to create schedule: %v", err)
	}

	// Reload schedules
	if err := orch.ReloadSchedules(ctx); err != nil {
		t.Fatalf("failed to reload schedules: %v", err)
	}

	// Tick scheduler
	orch.TickScheduler(ctx, time.Now().UTC())

	// Give a moment for asynchronous command handling
	time.Sleep(100 * time.Millisecond)

	// Verify agent record was created from schedule
	agents, err := st.ListAgents(ctx)
	if err != nil {
		t.Fatalf("failed to list agents: %v", err)
	}
	if len(agents) == 0 {
		t.Fatalf("expected scheduled agent to be created, got 0")
	}
	if agents[0].Harness != "codex" || agents[0].RepoID != "repo-orch-test" {
		t.Errorf("unexpected agent: %+v", agents[0])
	}
}
