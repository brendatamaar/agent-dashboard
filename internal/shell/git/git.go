package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"agent-dashboard/internal/core/repo"
)

type RepoStatus struct {
	CurrentBranch string `json:"current_branch"`
	IsDirty       bool   `json:"is_dirty"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
}

type Service struct {
	workspaceDir string
}

func NewService(workspaceDir string) *Service {
	return &Service{
		workspaceDir: workspaceDir,
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
	// If no upstream configured, compare to defaultBranch
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

// CreateWorktree creates an isolated worktree for an agent from a base branch.
func (s *Service) CreateWorktree(ctx context.Context, repoPath, baseBranch, newBranch, worktreePath string) error {
	if err := repo.ValidateBranchName(newBranch); err != nil {
		return fmt.Errorf("invalid branch name %q: %w", newBranch, err)
	}

	// Verify base branch exists
	verifyCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", baseBranch)
	if err := verifyCmd.Run(); err != nil {
		return fmt.Errorf("base branch %q does not exist: %w", baseBranch, err)
	}

	// Check if newBranch already exists
	checkCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", newBranch)
	var addArgs []string
	if checkCmd.Run() == nil {
		// Branch exists
		addArgs = []string{"-C", repoPath, "worktree", "add", worktreePath, newBranch}
	} else {
		// Create branch from baseBranch
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
	// 1. Remove worktree
	rmCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	var stderr bytes.Buffer
	rmCmd.Stderr = &stderr
	if err := rmCmd.Run(); err != nil {
		// If worktree removal failed, try pruning or removing dir directly if needed
		_ = os.RemoveAll(worktreePath)
	}

	// 2. Prune worktree metadata
	pruneCmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	_ = pruneCmd.Run()

	return nil
}
