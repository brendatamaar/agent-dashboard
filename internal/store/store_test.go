package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"
)

func TestStoreFullFlow(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	// 1. Initial setup status
	hasSetup, err := s.HasCompletedSetup(ctx)
	if err != nil {
		t.Fatalf("HasCompletedSetup error: %v", err)
	}
	if hasSetup {
		t.Fatal("expected setup not complete")
	}

	// 2. Create User
	salt, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt error: %v", err)
	}
	user := &store.User{
		ID:              "user-1",
		Username:        "admin",
		PasswordHash:    "hashed_password_xyz",
		TOTPSecret:      "JBSWY3DPEHPK3PXP",
		MasterSalt:      salt,
		IsSetupComplete: true,
	}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser error: %v", err)
	}

	hasSetup, err = s.HasCompletedSetup(ctx)
	if err != nil || !hasSetup {
		t.Fatalf("expected setup complete, got %v (err %v)", hasSetup, err)
	}

	fetchedUser, err := s.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetUserByUsername error: %v", err)
	}
	if fetchedUser.ID != "user-1" || fetchedUser.Username != "admin" {
		t.Fatalf("unexpected user fetched: %+v", fetchedUser)
	}

	// 3. Sessions
	token, err := s.CreateSession(ctx, user.ID, 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	sessionUser, err := s.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("ValidateSession error: %v", err)
	}
	if sessionUser.ID != user.ID {
		t.Fatalf("expected session user id %s, got %s", user.ID, sessionUser.ID)
	}

	if err := s.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession error: %v", err)
	}
	_, err = s.ValidateSession(ctx, token)
	if err == nil {
		t.Fatal("expected error validating deleted session")
	}

	// 4. Secrets
	key := crypto.DeriveKey("test-passphrase", salt)
	ciphertext, err := crypto.Encrypt([]byte("github_pat_secret123"), key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	if err := s.PutSecret(ctx, "sec-1", "github", "gh_token", ciphertext); err != nil {
		t.Fatalf("PutSecret error: %v", err)
	}

	sec, err := s.GetSecret(ctx, "gh_token")
	if err != nil {
		t.Fatalf("GetSecret error: %v", err)
	}
	decrypted, err := crypto.Decrypt(sec.Ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if string(decrypted) != "github_pat_secret123" {
		t.Fatalf("decrypted = %s, want github_pat_secret123", string(decrypted))
	}

	secList, err := s.ListSecrets(ctx)
	if err != nil || len(secList) != 1 {
		t.Fatalf("expected 1 secret in list, got %d (err %v)", len(secList), err)
	}

	// 5. Audit Log
	if err := s.WriteAudit(ctx, "admin", "login", "web", "success"); err != nil {
		t.Fatalf("WriteAudit error: %v", err)
	}
	if err := s.WriteAudit(ctx, "admin", "update_secret", "gh_token", "success"); err != nil {
		t.Fatalf("WriteAudit error: %v", err)
	}

	logs, err := s.GetAuditLogs(ctx, 10, 0)
	if err != nil {
		t.Fatalf("GetAuditLogs error: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 audit logs, got %d", len(logs))
	}
	if logs[0].Action != "update_secret" { // Ordered by id DESC
		t.Fatalf("expected latest log first, got %+v", logs[0])
	}
}

func TestStoreReposAndAgents(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_repo_agent.db")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	// 1. Repos
	repo := &store.Repo{
		ID:            "repo-1",
		Name:          "agent-dashboard",
		RemoteURL:     "https://github.com/brendatama/agent-dashboard.git",
		Path:          "/workspace/agent-dashboard",
		DefaultBranch: "main",
		SettingsJSON:  "{}",
	}
	if err := s.CreateRepo(ctx, repo); err != nil {
		t.Fatalf("CreateRepo error: %v", err)
	}

	// Test UpdateRepoSettings
	if err := s.UpdateRepoSettings(ctx, "repo-1", `{"runtime":"docker","auto_approve":true}`); err != nil {
		t.Fatalf("UpdateRepoSettings error: %v", err)
	}

	fetchedRepo, err := s.GetRepo(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepo error: %v", err)
	}
	if fetchedRepo.Name != "agent-dashboard" || fetchedRepo.SettingsJSON != `{"runtime":"docker","auto_approve":true}` {
		t.Fatalf("unexpected repo settings: %s", fetchedRepo.SettingsJSON)
	}

	// 2. Agents with Phase 3 fields
	agent := &store.AgentRecord{
		ID:            "agent-1",
		RepoID:        "repo-1",
		Harness:       "claude",
		Model:         "claude-sonnet-5-5",
		Runtime:       "docker",
		Mode:          "task",
		Branch:        "agent/test-task",
		WorktreePath:  "/workspace/agent-dashboard-wt-1",
		TmuxSession:   "tmux-agent-1",
		State:         "running",
		FailureReason: "",
		AutoApprove:   true,
	}
	if err := s.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("CreateAgent error: %v", err)
	}

	fetchedAgent, err := s.GetAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetAgent error: %v", err)
	}
	if !fetchedAgent.AutoApprove {
		t.Fatalf("expected AutoApprove true, got false")
	}

	agent.State = "failed"
	agent.FailureReason = "oom_killed"
	if err := s.UpdateAgent(ctx, agent); err != nil {
		t.Fatalf("UpdateAgent error: %v", err)
	}

	updatedAgent, err := s.GetAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetAgent error: %v", err)
	}
	if updatedAgent.FailureReason != "oom_killed" {
		t.Fatalf("expected FailureReason oom_killed, got %q", updatedAgent.FailureReason)
	}
}
