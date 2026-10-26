package repo

import (
	"testing"
)

func TestValidateRepoName(t *testing.T) {
	valid := []string{"agent-dashboard", "repo_123", "my.project", "tool-v2"}
	for _, name := range valid {
		if err := ValidateRepoName(name); err != nil {
			t.Errorf("expected valid repo name %q, got error: %v", name, err)
		}
	}

	invalid := []string{
		"",
		"-start-dash",
		".dotstart",
		"path/traversal",
		"path\\backslash",
		"has spaces",
		"semi;colon",
		"back`tick",
	}
	for _, name := range invalid {
		if err := ValidateRepoName(name); err == nil {
			t.Errorf("expected error for invalid repo name %q, got nil", name)
		}
	}
}

func TestParseAndValidateGitHubURL(t *testing.T) {
	cases := []struct {
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"https://github.com/brendatama/agent-dashboard", "brendatama", "agent-dashboard", false},
		{"https://github.com/brendatama/agent-dashboard.git", "brendatama", "agent-dashboard", false},
		{"git@github.com:brendatama/agent-dashboard.git", "brendatama", "agent-dashboard", false},
		{"https://gitlab.com/brendatama/agent-dashboard", "", "", true},
		{"https://github.com/brendatama", "", "", true},
		{"https://github.com/brendatama/repo/extra", "", "", true},
		{"invalid url", "", "", true},
	}

	for _, c := range cases {
		owner, repo, err := ParseAndValidateGitHubURL(c.url)
		if (err != nil) != c.wantErr {
			t.Errorf("URL %q: wantErr %v, got err: %v", c.url, c.wantErr, err)
		}
		if !c.wantErr {
			if owner != c.wantOwner || repo != c.wantRepo {
				t.Errorf("URL %q: got owner %q repo %q, want %q %q", c.url, owner, repo, c.wantOwner, c.wantRepo)
			}
		}
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"main", "feature/awesome-ui", "agent/fix-123", "bugfix_4.5"}
	for _, b := range valid {
		if err := ValidateBranchName(b); err != nil {
			t.Errorf("expected valid branch %q, got err: %v", b, err)
		}
	}

	invalid := []string{
		"",
		"/leading-slash",
		"trailing-slash/",
		".leading-dot",
		"contains..dotdot",
		"double//slash",
		"has space",
		"has~tilde",
		"has^caret",
		"has:colon",
		"has?question",
		"has*star",
		"has[bracket",
	}
	for _, b := range invalid {
		if err := ValidateBranchName(b); err == nil {
			t.Errorf("expected error for invalid branch %q, got nil", b)
		}
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Fix login bug #123!", "fix-login-bug-123"},
		{"Add Passkey Support (WebAuthn)", "add-passkey-support-webauthn"},
		{"   Multiple   Spaces   ", "multiple-spaces"},
		{"---dashes---", "dashes"},
		{"", "task"},
	}

	for _, c := range cases {
		got := SanitizeSlug(c.input)
		if got != c.want {
			t.Errorf("SanitizeSlug(%q) = %q; want %q", c.input, got, c.want)
		}
	}
}
