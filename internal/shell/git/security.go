package git

import (
	"agent-dashboard/internal/core/repo"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func canonicalGitHubURL(raw string) (string, error) {
	owner, name, err := repo.ParseAndValidateGitHubURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid canonical GitHub remote: %w", err)
	}
	return "https://github.com/" + owner + "/" + name + ".git", nil
}

// No token is placed in argv or persisted in the repository's config. The
// authentication header is scoped to the validated immutable GitHub URL.
func authenticatedGit(ctx context.Context, token, canonical string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 2 * time.Second
	env := gitEnvironment()
	config := [][2]string{{"credential.helper", ""}, {"http.extraHeader", ""}, {"http.followRedirects", "false"}, {"core.hooksPath", os.DevNull}, {"protocol.allow", "never"}, {"protocol.https.allow", "always"}}
	if token != "" {
		config = append(config, [2]string{"http." + canonical + ".extraHeader", "Authorization: Bearer " + token})
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
	for i, pair := range config {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, pair[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, pair[1]))
	}
	cmd.Env = env
	return cmd
}

func (s *Service) network(ctx context.Context, path, token string, remote []string, args ...string) error {
	if len(remote) != 1 {
		return fmt.Errorf("immutable canonical repository remote is required")
	}
	if err := s.ScrubPersistedCredentials(ctx, path); err != nil {
		return err
	}
	canonical, err := canonicalGitHubURL(remote[0])
	if err != nil {
		return err
	}
	// Reject redirects, alternate transports, headers, helpers, URL rewrites and
	// includes controlled by the agent before giving git any credential.
	check := authenticatedGit(ctx, "", canonical, "-C", path, "config", "--local", "--null", "--list")
	data, err := check.Output()
	if err != nil {
		return fmt.Errorf("inspect git configuration: %w", err)
	}
	for _, entry := range bytes.Split(data, []byte{0}) {
		key, _, _ := strings.Cut(string(entry), "\n")
		key = strings.ToLower(key)
		if strings.HasPrefix(key, "http.") || strings.HasPrefix(key, "credential.") || strings.HasPrefix(key, "url.") || strings.HasPrefix(key, "include.") || strings.HasPrefix(key, "includeif.") || strings.HasPrefix(key, "remote.origin.pushurl") {
			return fmt.Errorf("unsafe network configuration in repository")
		}
	}
	origin, err := authenticatedGit(ctx, "", canonical, "-C", path, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return fmt.Errorf("read origin: %w", err)
	}
	found, err := canonicalGitHubURL(strings.TrimSpace(string(origin)))
	if err != nil || found != canonical {
		return fmt.Errorf("repository origin differs from immutable remote")
	}
	// A private bare shadow prevents an agent racing the config check from
	// injecting a credential helper, URL rewrite, proxy or redirect after it.
	dir, err := os.MkdirTemp("", "agentd-git-network-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	shadow := filepath.Join(dir, "repo.git")
	if out, err := localGit(ctx, "clone", "--bare", "--shared", "--", path, shadow).CombinedOutput(); err != nil {
		return fmt.Errorf("prepare network checkout: %s: %w", out, err)
	}
	if err := os.WriteFile(filepath.Join(shadow, "config"), []byte("[core]\n\tbare = true\n\trepositoryformatversion = 0\n"), 0600); err != nil {
		return err
	}
	split := len(args)
	for i, arg := range args {
		if arg == "--" {
			split = i
			break
		}
	}
	operation := args[0]
	networkArgs := append([]string{}, args[:split]...)
	if operation == "pull" {
		networkArgs = []string{"fetch"}
	}
	full := []string{"--git-dir", shadow}
	full = append(full, networkArgs...)
	full = append(full, canonical)
	if operation == "fetch" {
		full = append(full, "+refs/heads/*:refs/remotes/origin/*")
	}
	if split < len(args) {
		full = append(full, args[split+1:]...)
	}
	transport := s.networkCommand
	if transport == nil {
		transport = authenticatedGit
	}
	cmd := transport(ctx, token, canonical, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := stderr.String()
		if token != "" {
			message = strings.ReplaceAll(message, token, "[REDACTED]")
		}
		return fmt.Errorf("git network operation failed: %s: %w", message, err)
	}
	runLocal := func(args ...string) error {
		if out, err := localGit(ctx, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("sync network result: %s: %w", out, err)
		}
		return nil
	}
	switch operation {
	case "push":
		branch := args[len(args)-1]
		if err := runLocal("-C", path, "config", "branch."+branch+".remote", "origin"); err != nil {
			return err
		}
		return runLocal("-C", path, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	case "fetch":
		return runLocal("-C", path, "fetch", "--no-tags", shadow, "+refs/remotes/origin/*:refs/remotes/origin/*")
	case "pull":
		commit, err := localGit(ctx, "--git-dir", shadow, "rev-parse", "FETCH_HEAD").Output()
		if err != nil {
			return err
		}
		if err := runLocal("-C", path, "fetch", "--no-tags", shadow, strings.TrimSpace(string(commit))); err != nil {
			return err
		}
		if len(args) > 1 && args[1] == "--rebase" {
			return runLocal("-C", path, "rebase", "FETCH_HEAD")
		}
		return runLocal("-C", path, "merge", "--ff-only", "FETCH_HEAD")
	}
	return fmt.Errorf("unsupported network operation")
}

// gitEnvironment excludes service encryption keys and provider credentials.
func gitEnvironment() []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "COMSPEC"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}
func localGit(ctx context.Context, args ...string) *exec.Cmd {
	argv := []string{"-c", "core.hooksPath=" + os.DevNull}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(gitEnvironment(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// ScrubPersistedCredentials migrates the old clone -c header bug. It never reads
// or returns the credential values, and removes every local authorization header.
func (s *Service) ScrubPersistedCredentials(ctx context.Context, path string) error {
	out, err := localGit(ctx, "-C", path, "config", "--local", "--name-only", "--get-regexp", `^http\..*extraheader$|^http\.extraheader$`).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("inspect legacy credentials: %w", err)
	}
	for _, key := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if key == "" {
			continue
		}
		if err := localGit(ctx, "-C", path, "config", "--local", "--unset-all", key).Run(); err != nil {
			return fmt.Errorf("remove legacy credentials: %w", err)
		}
	}
	return nil
}

func parsePorcelain(data []byte) ([]FileChange, error) {
	var changes []FileChange
	records := bytes.Split(data, []byte{0})
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) == 0 {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, fmt.Errorf("malformed porcelain record")
		}
		x, y := record[0], record[1]
		change := FileChange{Path: string(record[3:]), Staged: x != ' ' && x != '?', Unstaged: y != ' ' && y != '?'}
		switch {
		case x == 'R' || y == 'R' || x == 'C' || y == 'C':
			i++
			if i >= len(records) || len(records[i]) == 0 {
				return nil, fmt.Errorf("missing porcelain rename source")
			}
			change.OldPath = string(records[i])
			change.Status = "renamed"
		case x == '?' && y == '?':
			change.Status = "untracked"
			change.Unstaged = true
		case x == 'A' || y == 'A':
			change.Status = "added"
		case x == 'D' || y == 'D':
			change.Status = "deleted"
		default:
			change.Status = "modified"
		}
		changes = append(changes, change)
	}
	return changes, nil
}
