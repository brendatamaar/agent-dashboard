package gitguard

import (
	"errors"
	"fmt"
	"strings"

	"agent-dashboard/internal/core/repo"
)

var (
	ErrForcePushProtected     = errors.New("force-push to protected branches is forbidden")
	ErrProtectedBranchConfirm = errors.New("pushing to protected branch requires explicit confirmation")
	ErrInvalidBranchName      = errors.New("invalid branch name")
	ErrEmptyCommitMessage     = errors.New("commit message cannot be empty")
)

// ProtectedBranches lists default protected branch patterns.
var ProtectedBranches = []string{
	"main",
	"master",
	"production",
	"release/*",
}

// IsProtected checks if the given branch name matches any protected pattern.
func IsProtected(branch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false
	}
	for _, p := range ProtectedBranches {
		if strings.HasSuffix(p, "/*") {
			prefix := strings.TrimSuffix(p, "/*")
			if strings.HasPrefix(branch, prefix+"/") {
				return true
			}
		} else if strings.EqualFold(branch, p) {
			return true
		}
	}
	return false
}

// PushPolicy defines the safety rules for a git push.
type PushPolicy struct {
	ProtectedBranches []string
}

// DefaultPushPolicy returns the standard guardrail policy.
func DefaultPushPolicy() PushPolicy {
	return PushPolicy{
		ProtectedBranches: ProtectedBranches,
	}
}

// ValidatePush enforces:
// 1. Valid branch name.
// 2. Strict rejection of force-push to any protected branch.
// 3. Strict rejection of push to protected branches (e.g. main) unless confirmProtected is true.
func ValidatePush(branch string, isForce bool, confirmProtected bool) error {
	if err := repo.ValidateBranchName(branch); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBranchName, err)
	}

	if IsProtected(branch) {
		if isForce {
			return fmt.Errorf("%w: branch %q", ErrForcePushProtected, branch)
		}
		if !confirmProtected {
			return fmt.Errorf("%w: branch %q", ErrProtectedBranchConfirm, branch)
		}
	}

	return nil
}

// ValidateCommit checks that commit message is non-empty and reasonably sized.
func ValidateCommit(message string) error {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ErrEmptyCommitMessage
	}
	if len(trimmed) > 10000 {
		return fmt.Errorf("commit message exceeds maximum length of 10000 characters")
	}
	return nil
}
