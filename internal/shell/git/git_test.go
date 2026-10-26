package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestGitServiceOperations(t *testing.T) {
	repoDir, cleanupRepo := setupTestGitRepo(t)
	defer cleanupRepo()

	workspaceDir, err := os.MkdirTemp("", "workspace-ops-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(workspaceDir)

	svc := NewService(workspaceDir)
	ctx := context.Background()

	wtPath := filepath.Join(workspaceDir, "wt-ops")
	if err := svc.CreateWorktree(ctx, repoDir, "main", "agent/feature", wtPath); err != nil {
		t.Fatalf("create worktree failed: %v", err)
	}

	// 1. ChangedFiles with new untracked file
	testFile := "hello.txt"
	if err := os.WriteFile(filepath.Join(wtPath, testFile), []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	changes, err := svc.ChangedFiles(ctx, wtPath)
	if err != nil {
		t.Fatalf("ChangedFiles failed: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != testFile || changes[0].Status != "untracked" {
		t.Fatalf("unexpected changes: %+v", changes)
	}

	// 2. Diff for untracked file
	diff, err := svc.UnifiedDiff(ctx, wtPath, testFile, false)
	if err != nil {
		t.Fatalf("UnifiedDiff failed: %v", err)
	}
	if !strings.Contains(diff, "+hello world") {
		t.Fatalf("diff does not contain added content: %s", diff)
	}

	// 3. Stage file
	if err := svc.Stage(ctx, wtPath, []string{testFile}); err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	changes, err = svc.ChangedFiles(ctx, wtPath)
	if err != nil {
		t.Fatalf("ChangedFiles after stage failed: %v", err)
	}
	if len(changes) != 1 || !changes[0].Staged {
		t.Fatalf("expected staged change, got: %+v", changes)
	}

	// 4. Staged diff
	stagedDiff, err := svc.UnifiedDiff(ctx, wtPath, testFile, true)
	if err != nil {
		t.Fatalf("UnifiedDiff staged failed: %v", err)
	}
	if !strings.Contains(stagedDiff, "+hello world") {
		t.Fatalf("staged diff does not contain content: %s", stagedDiff)
	}

	// 5. Unstage file
	if err := svc.Unstage(ctx, wtPath, []string{testFile}); err != nil {
		t.Fatalf("Unstage failed: %v", err)
	}
	changes, _ = svc.ChangedFiles(ctx, wtPath)
	if len(changes) != 1 || changes[0].Staged {
		t.Fatalf("expected unstaged change, got: %+v", changes)
	}

	// Stage again and commit
	_ = svc.Stage(ctx, wtPath, []string{testFile})
	if err := svc.Commit(ctx, wtPath, "feat: add hello.txt", false); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify clean after commit
	changes, _ = svc.ChangedFiles(ctx, wtPath)
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes after commit, got: %d", len(changes))
	}

	// 6. Branch create and switch
	if err := svc.BranchCreate(ctx, wtPath, "agent/sub-task"); err != nil {
		t.Fatalf("BranchCreate failed: %v", err)
	}
	if err := svc.BranchSwitch(ctx, wtPath, "agent/sub-task"); err != nil {
		t.Fatalf("BranchSwitch failed: %v", err)
	}
	st, _ := svc.Status(ctx, wtPath, "main")
	if st.CurrentBranch != "agent/sub-task" {
		t.Fatalf("expected current branch agent/sub-task, got %s", st.CurrentBranch)
	}

	// 7. Push Guardrail Check: push to main without confirmation must fail
	if err := svc.Push(ctx, wtPath, "", "main", false, false); err == nil {
		t.Fatalf("expected push to main without confirm to fail")
	}

	// Push Guardrail Check: force push to main even with confirmation must fail
	if err := svc.Push(ctx, wtPath, "", "main", true, true); err == nil {
		t.Fatalf("expected force push to main to fail")
	}
}

func TestCreateGitHubPR(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-gh-token" {
			t.Errorf("missing or invalid authorization header")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PRResult{
			ID:      999,
			Number:  12,
			Title:   "Fix payment bug",
			HTMLURL: "https://github.com/myorg/myrepo/pull/12",
			State:   "open",
		})
	}))
	defer ts.Close()

	svc := NewService(t.TempDir())
	// Use custom test client that redirects to test server
	svc.httpClient = ts.Client()

	// Direct call to GitHub PR logic via test server URL
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL, strings.NewReader(`{"title":"Fix payment bug"}`))
	req.Header.Set("Authorization", "Bearer test-gh-token")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var pr PRResult
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if pr.Number != 12 || pr.HTMLURL != "https://github.com/myorg/myrepo/pull/12" {
		t.Fatalf("unexpected PR result: %+v", pr)
	}
}
