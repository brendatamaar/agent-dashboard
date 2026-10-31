package notifier

import (
	"agent-dashboard/internal/core/notification"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-dashboard/internal/store"
)

func TestNotifierVAPIDKeysInitialization(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	n, err := NewNotifier(st, nil)
	if err != nil {
		t.Fatalf("NewNotifier failed: %v", err)
	}

	pub := n.VAPIDPublicKey()
	if pub == "" {
		t.Fatalf("expected non-empty VAPID public key")
	}

	// Create second notifier using same store; should reuse stored VAPID keys
	n2, err := NewNotifier(st, nil)
	if err != nil {
		t.Fatalf("second NewNotifier failed: %v", err)
	}
	if n2.VAPIDPublicKey() != pub {
		t.Fatalf("expected second notifier to reuse public key %q, got %q", pub, n2.VAPIDPublicKey())
	}
}

func TestNotifierEncryptsAndMigratesExistingChannelSecrets(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	n, err := NewNotifier(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv := n.publicKey, n.privateKey
	oldKeys, _ := json.Marshal(map[string]string{"public": pub, "private": priv})
	settings := ChannelSettings{Telegram: TelegramConfig{Enabled: true, BotToken: "telegram-canary-123", ChatID: "test"}}
	oldSettings, _ := json.Marshal(settings)
	if err = st.PutSecret(ctx, "sec_vapid", "vapid_keys", "vapid_keys", oldKeys); err != nil {
		t.Fatal(err)
	}
	if err = st.PutSecret(ctx, "sec_settings", "notifier_settings", "notifier_settings", oldSettings); err != nil {
		t.Fatal(err)
	}
	migrated, err := NewNotifier(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.publicKey != pub || migrated.privateKey != priv {
		t.Fatal("migration rotated VAPID identity")
	}
	if migrated.Settings().Telegram.BotToken != "telegram-canary-123" {
		t.Fatal("migration lost channel token")
	}
	for _, name := range []string{"vapid_keys", "notifier_settings"} {
		sec, err := st.GetSecret(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if json.Valid(sec.Ciphertext) || bytes.Contains(sec.Ciphertext, []byte(priv)) || bytes.Contains(sec.Ciphertext, []byte("telegram-canary-123")) {
			t.Fatalf("%s remained plaintext", name)
		}
	}
	if err = migrated.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	sec, err := st.GetSecret(ctx, "notifier_settings")
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(sec.Ciphertext) || bytes.Contains(sec.Ciphertext, []byte(settings.Telegram.BotToken)) {
		t.Fatal("saved notifier settings exposed plaintext")
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	changed := settings
	changed.Telegram.BotToken = "unsaved-change"
	if err = migrated.SaveSettings(ctx, changed); err == nil {
		t.Fatal("closed store accepted settings")
	}
	if migrated.Settings().Telegram.BotToken != settings.Telegram.BotToken {
		t.Fatal("failed persistence changed effective settings")
	}
}

type recordingTransport struct{ calls int }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: req}, nil
}
func TestNotifierLocalizesQuietHoursBeforePurePolicy(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	n, err := NewNotifier(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	n.now = func() time.Time { return time.Date(2026, 10, 30, 16, 30, 0, 0, time.UTC) }
	transport := &recordingTransport{}
	n.SetHTTPClient(&http.Client{Transport: transport})
	cfg := ChannelSettings{Telegram: TelegramConfig{Enabled: true, BotToken: "local-canary", ChatID: "test"}, QuietHours: notification.QuietHoursConfig{Enabled: true, StartHour: 22, EndHour: 7, Timezone: "Asia/Jakarta"}}
	if err = n.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	n.NotifyAll(ctx, NotificationPayload{Title: "normal", Priority: notification.PriorityNormal})
	if transport.calls != 0 {
		t.Fatal("normal notification escaped local quiet hours")
	}
	n.NotifyAll(ctx, NotificationPayload{Title: "urgent", Priority: notification.PriorityUrgent})
	if transport.calls != 1 {
		t.Fatalf("urgent did not bypass quiet hours: calls=%d", transport.calls)
	}
}
