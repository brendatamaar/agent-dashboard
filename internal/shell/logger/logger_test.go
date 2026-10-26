package logger_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"agent-dashboard/internal/core/scrub"
	"agent-dashboard/internal/shell/logger"
)

func TestLoggerScrubsSecrets(t *testing.T) {
	var buf bytes.Buffer
	customSecret := "custom-secret-password-999"
	l := logger.New(&buf, slog.LevelInfo, customSecret)

	l.Info("Starting server with token", "token", "ghp_0123456789abcdef0123456789abcdef0123", "secret", customSecret)

	output := buf.String()
	if strings.Contains(output, "ghp_") {
		t.Fatalf("log contains unscrubbed github token: %s", output)
	}
	if strings.Contains(output, customSecret) {
		t.Fatalf("log contains unscrubbed custom secret: %s", output)
	}
	if !strings.Contains(output, scrub.RedactionPlaceholder) {
		t.Fatalf("log missing redaction placeholder: %s", output)
	}
}
