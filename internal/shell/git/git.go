package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agent-dashboard/internal/core/gitguard"
	"agent-dashboard/internal/core/repo"
)

type RepoStatus struct {
	CurrentBranch string `json:"current_branch"`
	IsDirty       bool   `json:"is_dirty"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
}

type FileChange struct {
	Path     string `json:"path"`
	OldPath  string `json:"old_path,omitempty"`
	Status   string `json:"status"` // "modified", "added", "deleted", "renamed", "untracked"
	Staged   bool   `json:"staged"`
	Unstaged bool   `json:"unstaged"`
}

type PRResult struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

type Service struct {
	workspaceDir string
	httpClient   *http.Client
}

func NewService(workspaceDir string) *Service {
	return &Service{
		workspaceDir: workspaceDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *Service) WorkspaceDir() string {
	return s.workspaceDir
}

// Clone clones a GitHub repository into the workspace directory using the provided token.
func (s *Service) Clone(ctx context.Context, remoteURL, token, targetName string) (string, error) {
	if err := repo.ValidateRepoName(targetName); err != nil {
		return "", fmt.Errorf("invalid target directory name: %w", err)
	}

	targetPath := filepath.Join(s.workspaceDir, targetName)
	if _, err := os.Stat(targetPath); err == nil {
		return "", fmt.Errorf("target path %q already exists", targetPath)
	}

	if err := os.MkdirAll(s.workspaceDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create workspace directory: %w", err)
	}

	args := []string{"clone"}
	if token != "" {
		args = append(args, "-c", fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token))
	}
	args = append(args, remoteURL, targetPath)

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git clone failed: %s: %w", stderr.String(), err)
	}

	return targetPath, nil
}

// Fetch fetches updates from origin.
func (s *Service) Fetch(ctx context.Context, repoPath, token string) error {
	args := []string{"-C", repoPath}
	if token != "" {
		args = append(args, "-c", fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token))
	}
	args = append(args, "fetch", "--all", "--prune")

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// Pull pulls changes fast-forward only.
func (s *Service) Pull(ctx context.Context, repoPath, token string) error {
	args := []string{"-C", repoPath}
	if token != "" {
		args = append(args, "-c", fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token))
	}
	args = append(args, "pull", "--ff-only")

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// Status inspects the branch, dirty state, and ahead/behind counts for a repository or worktree.
func (s *Service) Status(ctx context.Context, repoPath string, defaultBranch string) (RepoStatus, error) {
	var status RepoStatus

	// 1. Current branch
	branchCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.Output()
	if err != nil {
		return status, fmt.Errorf("failed to get current branch: %w", err)
	}
	status.CurrentBranch = strings.TrimSpace(string(branchOut))

	// 2. Dirty check
	dirtyCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "status", "--porcelain")
	dirtyOut, err := dirtyCmd.Output()
	if err != nil {
		return status, fmt.Errorf("failed to get status porcelain: %w", err)
	}
	status.IsDirty = len(bytes.TrimSpace(dirtyOut)) > 0

	// 3. Ahead / Behind
	targetRef := "@{upstream}"
	checkUpstreamCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "@{upstream}")
	if err := checkUpstreamCmd.Run(); err != nil {
		if defaultBranch != "" && defaultBranch != status.CurrentBranch {
			targetRef = defaultBranch
		} else {
			targetRef = ""
		}
	}

	if targetRef != "" {
		revCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-list", "--left-right", "--count", fmt.Sprintf("%s...%s", status.CurrentBranch, targetRef))
		revOut, err := revCmd.Output()
		if err == nil {
			parts := strings.Fields(strings.TrimSpace(string(revOut)))
			if len(parts) == 2 {
				status.Ahead, _ = strconv.Atoi(parts[0])
				status.Behind, _ = strconv.Atoi(parts[1])
			}
		}
	}

	return status, nil
}

// ChangedFiles returns the list of changed, staged, and untracked files in the worktree.
func (s *Service) ChangedFiles(ctx context.Context, worktreePath string) ([]FileChange, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain=v1", "-uall")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git status failed: %s: %w", stderr.String(), err)
	}

	var changes []FileChange
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		x := line[0] // staged
		y := line[1] // unstaged
		pathPart := strings.TrimSpace(line[3:])

		change := FileChange{
			Path: pathPart,
		}

		if strings.Contains(pathPart, " -> ") {
			parts := strings.Split(pathPart, " -> ")
			change.OldPath = parts[0]
			change.Path = parts[1]
			change.Status = "renamed"
			change.Staged = x == 'R'
			change.Unstaged = y == 'R'
		} else if x == '?' && y == '?' {
			change.Status = "untracked"
			change.Unstaged = true
		} else {
			change.Staged = (x != ' ' && x != '?')
			change.Unstaged = (y != ' ' && y != '?')

			if x == 'A' || y == 'A' {
				change.Status = "added"
			} else if x == 'D' || y == 'D' {
				change.Status = "deleted"
			} else {
				change.Status = "modified"
			}
		}

		changes = append(changes, change)
	}

	return changes, nil
}

// UnifiedDiff returns the unified diff for all changes or a specific file.
func (s *Service) UnifiedDiff(ctx context.Context, worktreePath, filePath string, staged bool) (string, error) {
	args := []string{"-C", worktreePath, "diff"}
	if staged {
		args = append(args, "--cached")
	}
	if filePath != "" {
		args = append(args, "--", filePath)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git diff failed: %s: %w", stderr.String(), err)
	}

	diffOut := stdout.String()

	// If untracked file diff requested and empty, read file as addition diff
	if diffOut == "" && filePath != "" && !staged {
		fullPath := filepath.Join(worktreePath, filePath)
		if fi, err := os.Stat(fullPath); err == nil && !fi.IsDir() {
			// Check if file is untracked
			checkCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain", "--", filePath)
			out, _ := checkCmd.Output()
			if strings.HasPrefix(string(out), "??") {
				content, err := os.ReadFile(fullPath)
				if err == nil {
					var sb strings.Builder
					sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
					sb.WriteString(fmt.Sprintf("new file mode 100644\n--- /dev/null\n+++ b/%s\n", filePath))
					lines := strings.Split(string(content), "\n")
					sb.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
					for _, l := range lines {
						sb.WriteString("+" + l + "\n")
					}
					return sb.String(), nil
				}
			}
		}
	}

	return diffOut, nil
}

// Stage stages the specified files. If paths is empty or contains ".", all changes are staged.
func (s *Service) Stage(ctx context.Context, worktreePath string, paths []string) error {
	args := []string{"-C", worktreePath, "add", "--"}
	if len(paths) == 0 {
		args = append(args, ".")
	} else {
		args = append(args, paths...)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// Unstage removes the specified files from staging.
func (s *Service) Unstage(ctx context.Context, worktreePath string, paths []string) error {
	args := []string{"-C", worktreePath, "restore", "--staged", "--"}
	if len(paths) == 0 {
		args = append(args, ".")
	} else {
		args = append(args, paths...)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Fallback for older git versions: git reset HEAD -- <paths>
		resetArgs := []string{"-C", worktreePath, "reset", "HEAD", "--"}
		if len(paths) == 0 {
			resetArgs = append(resetArgs, ".")
		} else {
			resetArgs = append(resetArgs, paths...)
		}
		resetCmd := exec.CommandContext(ctx, "git", resetArgs...)
		var resetErr bytes.Buffer
		resetCmd.Stderr = &resetErr
		if errReset := resetCmd.Run(); errReset != nil {
			return fmt.Errorf("git unstage failed: %s: %w", stderr.String(), err)
		}
	}
	return nil
}

// StageHunk applies a specific unified patch hunk to the git index.
func (s *Service) StageHunk(ctx context.Context, worktreePath string, patchContent string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "apply", "--cached", "--unidiff-zero", "-")
	cmd.Stdin = strings.NewReader(patchContent)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply hunk failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// Commit creates a new commit or amends the previous commit.
func (s *Service) Commit(ctx context.Context, worktreePath string, message string, amend bool) error {
	if err := gitguard.ValidateCommit(message); err != nil {
		return err
	}

	args := []string{"-C", worktreePath, "commit", "-m", message}
	if amend {
		args = append(args, "--amend")
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// BranchCreate creates a new branch from current HEAD.
func (s *Service) BranchCreate(ctx context.Context, worktreePath, branchName string) error {
	if err := repo.ValidateBranchName(branchName); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "branch", branchName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git branch create failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// BranchSwitch switches the active branch in the worktree.
func (s *Service) BranchSwitch(ctx context.Context, worktreePath, branchName string) error {
	if err := repo.ValidateBranchName(branchName); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "switch", branchName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Fallback to checkout if switch is not supported
		checkoutCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "checkout", branchName)
		var checkoutErr bytes.Buffer
		checkoutCmd.Stderr = &checkoutErr
		if errCheckout := checkoutCmd.Run(); errCheckout != nil {
			return fmt.Errorf("git branch switch failed: %s: %w", stderr.String(), err)
		}
	}
	return nil
}

// BranchDelete deletes a local branch. Protected branches are refused.
func (s *Service) BranchDelete(ctx context.Context, repoPath, branchName string, force bool) error {
	if gitguard.IsProtected(branchName) {
		return fmt.Errorf("cannot delete protected branch %q", branchName)
	}
	if err := repo.ValidateBranchName(branchName); err != nil {
		return err
	}

	flag := "-d"
	if force {
		flag = "-D"
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", flag, branchName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git branch delete failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// Push pushes the current branch to origin, validating against protected branch guardrails.
func (s *Service) Push(ctx context.Context, worktreePath, token, branch string, force, confirmMain bool) error {
	if err := gitguard.ValidatePush(branch, force, confirmMain); err != nil {
		return err
	}

	args := []string{"-C", worktreePath}
	if token != "" {
		args = append(args, "-c", fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token))
	}
	args = append(args, "push", "-u", "origin", branch)
	if force {
		args = append(args, "--force-with-lease")
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// PullRebase rebases the current branch onto origin/<baseBranch>.
func (s *Service) PullRebase(ctx context.Context, worktreePath, token, baseBranch string) error {
	if err := repo.ValidateBranchName(baseBranch); err != nil {
		return err
	}

	args := []string{"-C", worktreePath}
	if token != "" {
		args = append(args, "-c", fmt.Sprintf("http.extraHeader=Authorization: Bearer %s", token))
	}
	args = append(args, "pull", "--rebase", "origin", baseBranch)

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull --rebase failed: %s: %w", stderr.String(), err)
	}
	return nil
}

// CreateGitHubPR opens a pull request on GitHub using the dashboard's fine-grained token.
func (s *Service) CreateGitHubPR(ctx context.Context, token, remoteURL, title, body, headBranch, baseBranch string) (*PRResult, error) {
	if token == "" {
		return nil, fmt.Errorf("github token is required to open a pull request")
	}
	owner, repoName, err := repo.ParseAndValidateGitHubURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("invalid repository remote URL: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repoName)
	payload := map[string]string{
		"title": title,
		"body":  body,
		"head":  headBranch,
		"base":  baseBranch,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode PR payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agent-dashboard")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var pr PRResult
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	return &pr, nil
}

// CreateWorktree creates an isolated worktree for an agent from a base branch.
func (s *Service) CreateWorktree(ctx context.Context, repoPath, baseBranch, newBranch, worktreePath string) error {
	if err := repo.ValidateBranchName(newBranch); err != nil {
		return fmt.Errorf("invalid branch name %q: %w", newBranch, err)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for worktree: %w", err)
	}
	_ = os.RemoveAll(worktreePath)

	verifyCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", baseBranch)
	if err := verifyCmd.Run(); err != nil {
		return fmt.Errorf("base branch %q does not exist: %w", baseBranch, err)
	}

	checkCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", newBranch)
	var addArgs []string
	if checkCmd.Run() == nil {
		addArgs = []string{"-C", repoPath, "worktree", "add", worktreePath, newBranch}
	} else {
		addArgs = []string{"-C", repoPath, "worktree", "add", "-b", newBranch, worktreePath, baseBranch}
	}

	cmd := exec.CommandContext(ctx, "git", addArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree add failed: %s: %w", stderr.String(), err)
	}

	return nil
}

// RemoveWorktree deletes the agent worktree and optionally removes the agent branch.
func (s *Service) RemoveWorktree(ctx context.Context, repoPath, worktreePath, branch string) error {
	rmCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	var stderr bytes.Buffer
	rmCmd.Stderr = &stderr
	if err := rmCmd.Run(); err != nil {
		_ = os.RemoveAll(worktreePath)
	}

	pruneCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	_ = pruneCmd.Run()

	return nil
}
