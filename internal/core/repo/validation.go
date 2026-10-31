package repo

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	repoNameRegex   = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)
	branchSafeRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.\/]+$`)
)

// ValidateRepoName checks that a local repository name is safe for paths and identifiers.
func ValidateRepoName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("repository name cannot be empty")
	}
	if len(name) > 100 {
		return fmt.Errorf("repository name too long (max 100 characters)")
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("repository name cannot start with dot or hyphen")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("repository name contains forbidden path characters")
	}
	if !repoNameRegex.MatchString(name) {
		return fmt.Errorf("repository name %q contains invalid characters (allowed: alphanumeric, hyphen, underscore, dot)", name)
	}
	return nil
}

// ParseAndValidateGitHubURL parses a GitHub URL and returns the owner and repository name.
func ParseAndValidateGitHubURL(rawURL string) (owner, repo string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", fmt.Errorf("remote URL cannot be empty")
	}

	// Deliberately accept only the two canonical GitHub remote forms. URL
	// escapes, credentials, query strings, fragments and ports are rejected by
	// the repository-name grammar instead of normalized into a different remote.
	var path string
	switch {
	case strings.HasPrefix(rawURL, "https://github.com/"):
		path = strings.TrimPrefix(rawURL, "https://github.com/")
	case strings.HasPrefix(rawURL, "git@github.com:"):
		path = strings.TrimPrefix(rawURL, "git@github.com:")
	default:
		return "", "", fmt.Errorf("unsupported remote URL (expected HTTPS or SSH github.com owner/repo)")
	}
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub repository path (expected owner/repo)")
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")

	if err := ValidateRepoName(owner); err != nil {
		return "", "", fmt.Errorf("invalid repository owner %q: %w", owner, err)
	}
	if err := ValidateRepoName(repo); err != nil {
		return "", "", fmt.Errorf("invalid repository name %q: %w", repo, err)
	}

	return owner, repo, nil
}

// ValidateBranchName checks git branch name safety rules.
func ValidateBranchName(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if len(branch) > 200 {
		return fmt.Errorf("branch name too long (max 200 characters)")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("branch name cannot start with a hyphen")
	}
	for _, part := range strings.Split(branch, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("invalid branch component")
		}
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("branch name cannot begin or end with a slash")
	}
	if strings.HasPrefix(branch, ".") || strings.HasSuffix(branch, ".") {
		return fmt.Errorf("branch name cannot begin or end with a dot")
	}
	if strings.Contains(branch, "..") {
		return fmt.Errorf("branch name cannot contain '..'")
	}
	if strings.Contains(branch, "//") {
		return fmt.Errorf("branch name cannot contain '//'")
	}
	if strings.ContainsAny(branch, " ~^:?*[\t\r\n\\@{") {
		return fmt.Errorf("branch name contains forbidden characters")
	}
	if !branchSafeRegex.MatchString(branch) {
		return fmt.Errorf("branch name %q contains invalid characters", branch)
	}
	return nil
}

// SanitizeSlug converts a user-provided description or task name to a safe branch/worktree slug.
func SanitizeSlug(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var sb strings.Builder
	lastDash := false
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastDash = false
		} else if r == '-' || r == '_' || r == ' ' || r == '/' || r == '.' {
			if !lastDash && sb.Len() > 0 {
				sb.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(sb.String(), "-")
	if slug == "" {
		slug = "task"
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}
