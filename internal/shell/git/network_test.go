package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkUsesTrustedShadowDuringConfigRace(t *testing.T) {
	source, cleanup := setupTestGitRepo(t)
	defer cleanup()
	canonical := "https://github.com/owner/project.git"
	gitConfig(t, source, "remote.origin.url", canonical)
	svc := NewService(t.TempDir())
	ctx := context.Background()
	head, err := localGit(ctx, "-C", source, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	svc.networkCommand = func(ctx context.Context, token, remote string, args ...string) *exec.Cmd {
		called = true
		if token != "private-credential" || remote != canonical || len(args) < 4 || args[0] != "--git-dir" {
			t.Fatalf("wrong transport boundary: %v", args)
		}
		shadow := args[1]
		if shadow == filepath.Join(source, ".git") {
			t.Fatal("credential-bearing process uses agent metadata")
		}
		// Model the agent changing its config after origin validation.
		gitConfig(t, source, "credential.helper", "!echo must-never-run")
		data, err := os.ReadFile(filepath.Join(shadow, "config"))
		if err != nil || strings.Contains(string(data), "credential") || strings.Contains(string(data), "private-credential") {
			t.Fatalf("untrusted config reached transport: %q %v", data, err)
		}
		// Simulate only the remote transfer with real Git in the isolated repository.
		// The rest of Service.Fetch (including local result synchronization) is live.
		return authenticatedGit(ctx, token, remote, "--git-dir", shadow, "update-ref", "refs/remotes/origin/main", strings.TrimSpace(string(head)))
	}
	if err := svc.Fetch(ctx, source, "private-credential", canonical); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("network transport was not exercised")
	}
	data, err := localGit(ctx, "-C", source, "rev-parse", "refs/remotes/origin/main").Output()
	if err != nil || string(data) != string(head) {
		t.Fatalf("network refs not synchronized: %s %v", data, err)
	}
	config, _ := os.ReadFile(filepath.Join(source, ".git", "config"))
	if strings.Contains(string(config), "private-credential") {
		t.Fatal("credential persisted in source checkout")
	}
}
func TestGitCommandsDoNotInheritServiceSecrets(t *testing.T) {
	t.Setenv("AGENTD_MASTER_KEY", "dashboard-private")
	t.Setenv("OPENAI_API_KEY", "provider-private")
	for _, cmd := range []*exec.Cmd{localGit(context.Background(), "status"), authenticatedGit(context.Background(), "selected-token", "https://github.com/owner/project.git", "fetch")} {
		env := strings.Join(cmd.Env, "\n")
		if strings.Contains(env, "dashboard-private") || strings.Contains(env, "provider-private") {
			t.Fatal("git inherited dashboard credentials")
		}
	}
}
