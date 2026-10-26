package scrub

import (
	"regexp"
	"strings"
)

// Known secret token patterns.
var (
	githubPatRegex   = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,255}`)
	githubFineRegex  = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{60,255}`)
	anthropicRegex   = regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{30,255}`)
	openaiRegex      = regexp.MustCompile(`sk-[A-Za-z0-9]{32,255}`)
	genericAuthRegex = regexp.MustCompile(`(?i)(bearer\s+|token\s+|password\s*[:=]\s*)[A-Za-z0-9_\-\.]{16,}`)
)

// RedactionPlaceholder is used to replace sensitive secrets.
const RedactionPlaceholder = "[REDACTED_SECRET]"

// Scrub removes known token patterns and custom secret values from a string.
func Scrub(text string, customSecrets ...string) string {
	if text == "" {
		return ""
	}

	result := text

	// Scrub any explicitly registered custom secrets first
	for _, secret := range customSecrets {
		trimmed := strings.TrimSpace(secret)
		if len(trimmed) >= 4 { // Only scrub non-trivial secrets
			result = strings.ReplaceAll(result, trimmed, RedactionPlaceholder)
		}
	}

	// Scrub known vendor token patterns
	result = githubFineRegex.ReplaceAllString(result, RedactionPlaceholder)
	result = githubPatRegex.ReplaceAllString(result, RedactionPlaceholder)
	result = anthropicRegex.ReplaceAllString(result, RedactionPlaceholder)
	result = openaiRegex.ReplaceAllString(result, RedactionPlaceholder)

	// Scrub generic auth tokens preserving prefix
	result = genericAuthRegex.ReplaceAllStringFunc(result, func(match string) string {
		parts := strings.SplitN(match, " ", 2)
		if len(parts) == 2 {
			return parts[0] + " " + RedactionPlaceholder
		}
		parts = strings.SplitN(match, ":", 2)
		if len(parts) == 2 {
			return parts[0] + ":" + RedactionPlaceholder
		}
		parts = strings.SplitN(match, "=", 2)
		if len(parts) == 2 {
			return parts[0] + "=" + RedactionPlaceholder
		}
		return RedactionPlaceholder
	})

	return result
}
