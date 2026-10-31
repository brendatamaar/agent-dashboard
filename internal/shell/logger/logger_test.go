package logger_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestRecursiveAnyErrorAndDynamicSecrets(t *testing.T) {
	var buf bytes.Buffer
	l := logger.New(&buf, slog.LevelInfo)
	secret := "custom\nsecret-canary"
	derived := l.With("component", "worker", "bound_secret", secret)
	logger.RegisterSecrets(l, secret)
	derived.Error("failure", "error", fmt.Errorf("provider: %s", secret), "nested", map[string]any{"items": []any{map[string]string{"credential": secret}}})
	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(record), secret) {
		t.Fatalf("nested/error value leaked: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "REDACTED") {
		t.Fatal("redaction marker absent")
	}
	if logger.Scrub(derived, secret) == secret {
		t.Fatal("derived logger did not share dynamic registry")
	}
}
