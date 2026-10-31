package gitguard_test

import (
	"errors"
	"testing"

	"agent-dashboard/internal/core/gitguard"
)

func TestIsProtected(t *testing.T) {
	tests := []struct {
		branch    string
		protected bool
	}{
		{"main", true},
		{"MAIN", true},
		{"master", true},
		{"production", true},
		{"release/1.0", true},
		{"release/v2.1.0", true},
		{"feat/branch", false},
		{"agent/my-task", false},
		{"fix/bug-123", false},
		{"", false},
	}

	for _, tc := range tests {
		got := gitguard.IsProtected(tc.branch)
		if got != tc.protected {
			t.Errorf("IsProtected(%q) = %v; want %v", tc.branch, got, tc.protected)
		}
	}
}

func TestValidatePush(t *testing.T) {
	tests := []struct {
		name             string
		branch           string
		isForce          bool
		confirmProtected bool
		wantErr          error
	}{
		{
			name:             "normal feature branch push",
			branch:           "feat/awesome-feature",
			isForce:          false,
			confirmProtected: false,
			wantErr:          nil,
		},
		{
			name:             "normal feature branch force push",
			branch:           "feat/awesome-feature",
			isForce:          true,
			confirmProtected: false,
			wantErr:          nil,
		},
		{
			name:             "main push refused without confirmation",
			branch:           "main",
			isForce:          false,
			confirmProtected: false,
			wantErr:          gitguard.ErrProtectedBranchConfirm,
		},
		{
			name:             "main push allowed with confirmation",
			branch:           "main",
			isForce:          false,
			confirmProtected: true,
			wantErr:          nil,
		},
		{
			name:             "main force push refused even with confirmation",
			branch:           "main",
			isForce:          true,
			confirmProtected: true,
			wantErr:          gitguard.ErrForcePushProtected,
		},
		{
			name:             "release branch force push refused",
			branch:           "release/v1.0",
			isForce:          true,
			confirmProtected: true,
			wantErr:          gitguard.ErrForcePushProtected,
		},
		{
			name:             "release branch push refused without confirmation",
			branch:           "release/v1.0",
			isForce:          false,
			confirmProtected: false,
			wantErr:          gitguard.ErrProtectedBranchConfirm,
		},
		{
			name:             "invalid branch name refused",
			branch:           "invalid branch name with spaces",
			isForce:          false,
			confirmProtected: false,
			wantErr:          gitguard.ErrInvalidBranchName,
		},
		{
			name:             "branch with path traversal refused",
			branch:           "feat/../main",
			isForce:          false,
			confirmProtected: false,
			wantErr:          gitguard.ErrInvalidBranchName,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := gitguard.ValidatePush(tc.branch, tc.isForce, tc.confirmProtected)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want to wrap %v", err, tc.wantErr)
				}
			}
		})
	}
}

func TestValidateCommit(t *testing.T) {
	if err := gitguard.ValidateCommit("feat: initial commit"); err != nil {
		t.Errorf("unexpected error for valid commit: %v", err)
	}

	if err := gitguard.ValidateCommit("   "); !errors.Is(err, gitguard.ErrEmptyCommitMessage) {
		t.Errorf("expected ErrEmptyCommitMessage for whitespace, got: %v", err)
	}

	if err := gitguard.ValidateCommit(""); !errors.Is(err, gitguard.ErrEmptyCommitMessage) {
		t.Errorf("expected ErrEmptyCommitMessage for empty, got: %v", err)
	}
}

func TestDefaultPushPolicyCannotMutateGlobalGuards(t *testing.T) {
	p := gitguard.DefaultPushPolicy()
	p.ProtectedBranches[0] = "unprotected"
	if !gitguard.IsProtected("main") || gitguard.ValidatePush("main", false, false) == nil {
		t.Fatal("caller mutated protected branch security policy")
	}
	if gitguard.DefaultPushPolicy().ProtectedBranches[0] != "main" {
		t.Fatal("default policy shares mutable storage")
	}
}
