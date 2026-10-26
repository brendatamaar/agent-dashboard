package scrub_test

import (
	"testing"

	"agent-dashboard/internal/core/scrub"
)

func TestScrubKnownTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		secrets  []string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "plain text without secrets",
			input:    "starting agent-dashboard on 127.0.0.1:8080",
			expected: "starting agent-dashboard on 127.0.0.1:8080",
		},
		{
			name:     "github classic token",
			input:    "git clone https://ghp_0123456789abcdef0123456789abcdef0123@github.com/repo.git",
			expected: "git clone https://" + scrub.RedactionPlaceholder + "@github.com/repo.git",
		},
		{
			name:     "github fine-grained token",
			input:    "export GITHUB_TOKEN=github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP",
			expected: "export GITHUB_TOKEN=" + scrub.RedactionPlaceholder,
		},
		{
			name:     "anthropic api key",
			input:    "connecting with key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789_AA",
			expected: "connecting with key " + scrub.RedactionPlaceholder,
		},
		{
			name:     "custom dynamic secret",
			input:    "secret passphrase is super-secret-passphrase-xyz here",
			secrets:  []string{"super-secret-passphrase-xyz"},
			expected: "secret passphrase is " + scrub.RedactionPlaceholder + " here",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scrub.Scrub(tc.input, tc.secrets...)
			if got != tc.expected {
				t.Errorf("Scrub(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
