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

type dummyDecryptor struct{}

func (dummyDecryptor) DecryptSecret(ctx context.Context, name string) (string, error) {
	return "test-token", nil
}

func TestOrchestratorLifecycleAndReconcile(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_orch.db")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store error: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Create test repo in store
	repoRecord := &store.Repo{
		ID:            "repo-orch-1",
		Name:          "orch-repo",
		RemoteURL:     "https://github.com/brendatama/orch-repo.git",
		Path:          filepath.Join(tempDir, "orch-repo"),
		DefaultBranch: "main",
	}
	if err := s.CreateRepo(ctx, repoRecord); err != nil {
		t.Fatalf("create repo error: %v", err)
	}

	gitSvc := git.NewService(tempDir)
	fakeRt := runtime.NewFakeRuntime()
	orch := orchestrator.NewOrchestrator(s, gitSvc, fakeRt, dummyDecryptor{}, nil, 1)
	defer func() { _ = orch.Shutdown(context.Background()) }()

	// 1. Initial Start
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("orchestrator start error: %v", err)
	}

	// 2. Submit Agent
	agentObj, err := orch.SubmitAgent(ctx, "repo-orch-1", "claude", "claude-sonnet-5-5", "task", "Test prompt", "main", "")
	if err != nil {
		t.Fatalf("submit agent error: %v", err)
	}
	if agentObj.ID == "" {
		t.Fatalf("expected non-empty agent id")
	}

	// Give background command execution a moment
	time.Sleep(50 * time.Millisecond)

	// Verify agent exists
	a, ok := orch.GetAgent(agentObj.ID)
	if !ok {
		t.Fatalf("agent %s not found in orchestrator", agentObj.ID)
	}
	if a.ID != agentObj.ID {
		t.Fatalf("agent id mismatch: %s vs %s", a.ID, agentObj.ID)
	}

	// 3. Stop Agent
	if err := orch.StopAgent(ctx, agentObj.ID); err != nil {
		t.Fatalf("stop agent error: %v", err)
	}

	// 4. Test boot re-attach on restart
	if err := orch.Shutdown(ctx); err != nil {
		t.Fatalf("orchestrator shutdown error: %v", err)
	}
	orch2 := orchestrator.NewOrchestrator(s, gitSvc, fakeRt, dummyDecryptor{}, nil, 1)
	defer func() { _ = orch2.Shutdown(context.Background()) }()
	if err := orch2.Start(ctx); err != nil {
		t.Fatalf("orchestrator 2 start error: %v", err)
	}

	agents := orch2.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent reloaded from database, got %d", len(agents))
	}
}
