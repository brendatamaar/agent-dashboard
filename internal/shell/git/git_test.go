package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupTestGitRepo(t *testing.T) (string, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s", args, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@example.com")
	runGit("branch", "-M", "main")

	// Create initial commit
	readmePath := filepath.Join(tempDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "Initial commit")

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	return tempDir, cleanup
}

func TestGitServiceWorktreeAndStatus(t *testing.T) {
	repoDir, cleanupRepo := setupTestGitRepo(t)
	defer cleanupRepo()

	workspaceDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(workspaceDir)

	svc := NewService(workspaceDir)
	ctx := context.Background()

	// Check clean status
	status, err := svc.Status(ctx, repoDir, "main")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.CurrentBranch != "main" {
		t.Fatalf("expected main branch, got %s", status.CurrentBranch)
	}
	if status.IsDirty {
		t.Fatalf("expected clean status")
	}

	// Create a worktree
	wtPath := filepath.Join(workspaceDir, "wt-agent-1")
	newBranch := "agent/task-42"
	if err := svc.CreateWorktree(ctx, repoDir, "main", newBranch, wtPath); err != nil {
		t.Fatalf("create worktree failed: %v", err)
	}

	// Check worktree status
	wtStatus, err := svc.Status(ctx, wtPath, "main")
	if err != nil {
		t.Fatalf("worktree status failed: %v", err)
	}
	if wtStatus.CurrentBranch != newBranch {
		t.Fatalf("expected branch %s, got %s", newBranch, wtStatus.CurrentBranch)
	}
	if wtStatus.IsDirty {
		t.Fatalf("expected clean worktree")
	}

	// Make worktree dirty
	dirtyFile := filepath.Join(wtPath, "newfile.txt")
	if err := os.WriteFile(dirtyFile, []byte("change"), 0644); err != nil {
		t.Fatalf("write dirty file failed: %v", err)
	}
	wtStatusDirty, err := svc.Status(ctx, wtPath, "main")
	if err != nil {
		t.Fatalf("worktree status failed: %v", err)
	}
	if !wtStatusDirty.IsDirty {
		t.Fatalf("expected worktree to be dirty")
	}

	// Remove worktree
	if err := svc.RemoveWorktree(ctx, repoDir, wtPath, newBranch); err != nil {
		t.Fatalf("remove worktree failed: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to be removed")
	}
}
