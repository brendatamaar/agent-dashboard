package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitConfig(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path, "config"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config: %s %v", out, err)
	}
}
func TestWorktreeNeverDeletesExistingData(t *testing.T) {
	source, cleanup := setupTestGitRepo(t)
	defer cleanup()
	root := t.TempDir()
	path := filepath.Join(root, "existing")
	_ = os.Mkdir(path, 0700)
	file := filepath.Join(path, "sentinel")
	_ = os.WriteFile(file, []byte("precious"), 0600)
	svc := NewService(root)
	if err := svc.CreateWorktree(context.Background(), source, "main", "agent/a", path); err == nil {
		t.Fatal("accepted existing checkout")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "precious" {
		t.Fatalf("deleted existing work: %q %v", data, err)
	}
}
func TestWorktreeOwnMetadataAndSafeConfig(t *testing.T) {
	source, cleanup := setupTestGitRepo(t)
	defer cleanup()
	root := t.TempDir()
	path := filepath.Join(root, "agent-a")
	ctx := context.Background()
	svc := NewService(root)
	gitConfig(t, source, "http.extraHeader", "Authorization: Bearer must-not-copy")
	gitConfig(t, source, "remote.origin.url", "https://github.com/owner/project.git")
	if err := svc.CreateWorktree(ctx, source, "main", "agent/a", path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !info.IsDir() {
		t.Fatalf("git metadata is not standalone: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(path, ".git", "config"))
	if err != nil || strings.Contains(string(data), "must-not-copy") || strings.Contains(string(data), source) {
		t.Fatalf("dashboard metadata leaked into checkout: %q %v", data, err)
	}
	if err := svc.EnsureWorktree(ctx, source, "main", "agent/a", path); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := svc.EnsureWorktree(ctx, source, "main", "agent/b", path); err == nil {
		t.Fatal("accepted another attempt's checkout")
	}
	if err := svc.RemoveWorktree(ctx, source, source, "main"); err == nil {
		t.Fatal("cleanup accepted external path")
	}
}
func TestNetworkRejectsMutableOriginsAndCredentialConfig(t *testing.T) {
	source, cleanup := setupTestGitRepo(t)
	defer cleanup()
	svc := NewService(t.TempDir())
	ctx := context.Background()
	canonical := "https://github.com/owner/project.git"
	gitConfig(t, source, "remote.origin.url", "https://example.invalid/steal")
	if err := svc.Fetch(ctx, source, "super-secret", canonical); err == nil {
		t.Fatal("accepted changed origin")
	}
	gitConfig(t, source, "remote.origin.url", canonical)
	for _, key := range []string{"credential.helper", "url.https://evil.invalid/.insteadof", "remote.origin.pushurl", "include.path"} {
		gitConfig(t, source, key, "malicious")
		if err := svc.Push(ctx, source, "super-secret", "agent/a", false, false, canonical); err == nil {
			t.Errorf("accepted unsafe config %s", key)
		}
		gitConfig(t, source, "--unset-all", key)
	}
	if err := svc.Fetch(ctx, source, "super-secret"); err == nil {
		t.Fatal("accepted missing immutable URL")
	}
}
func TestCredentialIsTransientAndOriginScoped(t *testing.T) {
	cmd := authenticatedGit(context.Background(), "super-secret", "https://github.com/owner/project.git", "clone", "https://github.com/owner/project.git")
	if strings.Contains(strings.Join(cmd.Args, " "), "super-secret") {
		t.Fatal("credential leaked into argv")
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "http.https://github.com/owner/project.git.extraHeader") || !strings.Contains(env, "Authorization: Bearer super-secret") {
		t.Fatal("missing origin-scoped transient auth")
	}
	if !strings.Contains(env, "http.followRedirects") || !strings.Contains(env, "GIT_CONFIG_GLOBAL=") {
		t.Fatal("missing transport confinement")
	}
}
func TestPorcelainPreservesPathBytes(t *testing.T) {
	data := []byte("??  leading space -> name\nwith\ttab\x00R  new -> name\x00old\nname\x00")
	changes, err := parsePorcelain(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Path != " leading space -> name\nwith\ttab" || changes[0].Status != "untracked" || changes[1].Path != "new -> name" || changes[1].OldPath != "old\nname" {
		t.Fatalf("porcelain paths changed: %+v", changes)
	}
	if _, err := parsePorcelain([]byte("R  target\x00")); err == nil {
		t.Fatal("accepted truncated rename")
	}
}

func TestLegacyCloneCredentialMigration(t *testing.T) {
	source, cleanup := setupTestGitRepo(t)
	defer cleanup()
	svc := NewService(t.TempDir())
	gitConfig(t, source, "http.extraHeader", "Authorization: Bearer legacy-secret")
	if err := svc.ScrubPersistedCredentials(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(source, ".git", "config"))
	if err != nil || strings.Contains(string(data), "legacy-secret") {
		t.Fatalf("legacy credential remains: %v", err)
	}
}
