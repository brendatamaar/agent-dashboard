package notifier_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent-dashboard/internal/core/notification"
	"agent-dashboard/internal/shell/notifier"
	"agent-dashboard/internal/store"
)

func TestNotifierChannelSettingsAndQuietHours(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	n, err := notifier.NewNotifier(st, nil)
	if err != nil {
		t.Fatalf("failed to create notifier: %v", err)
	}

	settings := notifier.ChannelSettings{
		Telegram: notifier.TelegramConfig{
			Enabled:  true,
			BotToken: "test-bot-token",
			ChatID:   "12345678",
		},
		Ntfy: notifier.NtfyConfig{
			Enabled:   true,
			ServerURL: "https://ntfy.sh",
			Topic:     "test-topic",
		},
		QuietHours: notification.QuietHoursConfig{
			Enabled:   true,
			StartHour: 22,
			StartMin:  0,
			EndHour:   7,
			EndMin:    0,
			Timezone:  "UTC",
		},
	}

	if err := n.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	loaded := n.Settings()
	if !loaded.Telegram.Enabled || loaded.Telegram.ChatID != "12345678" {
		t.Errorf("telegram settings mismatch: %+v", loaded.Telegram)
	}
	if !loaded.Ntfy.Enabled || loaded.Ntfy.Topic != "test-topic" {
		t.Errorf("ntfy settings mismatch: %+v", loaded.Ntfy)
	}
	if !loaded.QuietHours.Enabled || loaded.QuietHours.StartHour != 22 {
		t.Errorf("quiet hours settings mismatch: %+v", loaded.QuietHours)
	}

	// NotifyAll during daytime (safe) should not crash even if network calls fail against mock tokens
	n.NotifyAll(ctx, notifier.NotificationPayload{
		Title:    "Test alert",
		Body:     "Test body",
		Priority: notification.PriorityNormal,
	})
	_ = time.Now()
}
