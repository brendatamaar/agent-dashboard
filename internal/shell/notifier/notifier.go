package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent-dashboard/internal/core/notification"
	"agent-dashboard/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
)

type NotificationPayload struct {
	Title     string                `json:"title"`
	Body      string                `json:"body"`
	TargetURL string                `json:"target_url,omitempty"`
	Tag       string                `json:"tag,omitempty"`
	Icon      string                `json:"icon,omitempty"`
	Priority  notification.Priority `json:"priority,omitempty"`
}

type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type NtfyConfig struct {
	Enabled   bool   `json:"enabled"`
	ServerURL string `json:"server_url"` // default https://ntfy.sh
	Topic     string `json:"topic"`
}

type ChannelSettings struct {
	Telegram   TelegramConfig                `json:"telegram"`
	Ntfy       NtfyConfig                    `json:"ntfy"`
	QuietHours notification.QuietHoursConfig `json:"quiet_hours"`
}

type Notifier struct {
	store      *store.Store
	logger     *slog.Logger
	publicKey  string
	privateKey string
	settings   ChannelSettings
	httpClient *http.Client
	mu         sync.RWMutex
}

func NewNotifier(st *store.Store, logger *slog.Logger) (*Notifier, error) {
	if logger == nil {
		logger = slog.Default()
	}

	n := &Notifier{
		store:  st,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// Initialize VAPID keys from store or generate new ones
	if err := n.ensureVAPIDKeys(); err != nil {
		return nil, fmt.Errorf("failed to initialize VAPID keys: %w", err)
	}

	// Load channel settings from store if saved
	n.loadSettings()

	return n, nil
}

func (n *Notifier) loadSettings() {
	ctx := context.Background()
	sec, err := n.store.GetSecret(ctx, "notifier_settings")
	if err == nil && len(sec.Ciphertext) > 0 {
		var s ChannelSettings
		if err := json.Unmarshal(sec.Ciphertext, &s); err == nil {
			n.mu.Lock()
			n.settings = s
			n.mu.Unlock()
		}
	}
}

func (n *Notifier) SaveSettings(ctx context.Context, s ChannelSettings) error {
	n.mu.Lock()
	n.settings = s
	n.mu.Unlock()

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return n.store.PutSecret(ctx, "sec_notifier", "notifier_settings", "notifier_settings", data)
}

func (n *Notifier) Settings() ChannelSettings {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.settings
}

func (n *Notifier) ensureVAPIDKeys() error {
	ctx := context.Background()
	sec, err := n.store.GetSecret(ctx, "vapid_keys")
	if err == nil && len(sec.Ciphertext) > 0 {
		var keys struct {
			Public  string `json:"public"`
			Private string `json:"private"`
		}
		if err := json.Unmarshal(sec.Ciphertext, &keys); err == nil && keys.Public != "" && keys.Private != "" {
			n.publicKey = keys.Public
			n.privateKey = keys.Private
			return nil
		}
	}

	// Generate new VAPID keys
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("failed to generate VAPID keys: %w", err)
	}

	keysJSON, err := json.Marshal(map[string]string{
		"public":  publicKey,
		"private": privateKey,
	})
	if err != nil {
		return err
	}

	if err := n.store.PutSecret(ctx, "sec_vapid", "vapid_keys", "vapid_keys", keysJSON); err != nil {
		n.logger.Warn("Failed to persist VAPID keys to store, using ephemeral keys", "error", err)
	}

	n.publicKey = publicKey
	n.privateKey = privateKey
	n.logger.Info("Initialized Web Push VAPID keys", "publicKey", publicKey)
	return nil
}

func (n *Notifier) VAPIDPublicKey() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.publicKey
}

// Send sends a push notification to a specific Web Push subscription.
func (n *Notifier) Send(ctx context.Context, sub store.PushSubscription, payload NotificationPayload) error {
	n.mu.RLock()
	pub := n.publicKey
	priv := n.privateKey
	n.mu.RUnlock()

	if pub == "" || priv == "" {
		return fmt.Errorf("VAPID keys not configured")
	}

	if payload.Icon == "" {
		payload.Icon = "/icon.svg"
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal push payload: %w", err)
	}

	s := webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}

	resp, err := webpush.SendNotification(payloadBytes, &s, &webpush.Options{
		Subscriber:      "mailto:admin@agent-dashboard.local",
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		TTL:             86400,
	})
	if err != nil {
		return fmt.Errorf("webpush send failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		// Subscription is expired or invalid; clean up from database
		n.logger.Info("Cleaning up expired push subscription", "endpoint", sub.Endpoint)
		_ = n.store.DeletePushSubscription(ctx, sub.Endpoint)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push service returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// SendTelegram sends an alert via Telegram Bot API.
func (n *Notifier) SendTelegram(ctx context.Context, payload NotificationPayload) error {
	n.mu.RLock()
	cfg := n.settings.Telegram
	n.mu.RUnlock()

	if !cfg.Enabled || cfg.BotToken == "" || cfg.ChatID == "" {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	text := fmt.Sprintf("*%s*\n%s", payload.Title, payload.Body)
	if payload.TargetURL != "" {
		text += fmt.Sprintf("\n[Open Dashboard](%s)", payload.TargetURL)
	}

	bodyData, _ := json.Marshal(map[string]string{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "Markdown",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendNtfy publishes a notification to an ntfy topic.
func (n *Notifier) SendNtfy(ctx context.Context, payload NotificationPayload) error {
	n.mu.RLock()
	cfg := n.settings.Ntfy
	n.mu.RUnlock()

	if !cfg.Enabled || cfg.Topic == "" {
		return nil
	}

	serverURL := strings.TrimRight(cfg.ServerURL, "/")
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	targetURL := fmt.Sprintf("%s/%s", serverURL, cfg.Topic)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(payload.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", payload.Title)
	if payload.TargetURL != "" {
		req.Header.Set("Click", payload.TargetURL)
	}
	if payload.Priority == notification.PriorityUrgent {
		req.Header.Set("Priority", "high")
	}

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// NotifyAll dispatches the notification to all registered push subscribers and configured channels.
func (n *Notifier) NotifyAll(ctx context.Context, payload NotificationPayload) {
	n.mu.RLock()
	qh := n.settings.QuietHours
	n.mu.RUnlock()

	// Quiet hours check
	now := time.Now().UTC()
	priority := payload.Priority
	if priority == "" {
		priority = notification.PriorityNormal
	}

	if !notification.ShouldDeliver(now, priority, qh) {
		n.logger.Info("Notification silenced by quiet hours", "title", payload.Title, "priority", priority)
		return
	}

	// 1. Web Push
	subs, err := n.store.ListPushSubscriptions(ctx)
	if err != nil {
		n.logger.Error("Failed to list push subscriptions", "error", err)
	} else {
		for _, sub := range subs {
			if err := n.Send(ctx, sub, payload); err != nil {
				n.logger.Warn("Failed to send push notification", "endpoint", sub.Endpoint, "error", err)
			}
		}
	}

	// 2. Telegram
	if err := n.SendTelegram(ctx, payload); err != nil {
		n.logger.Warn("Failed to send Telegram notification", "error", err)
	}

	// 3. Ntfy
	if err := n.SendNtfy(ctx, payload); err != nil {
		n.logger.Warn("Failed to send ntfy notification", "error", err)
	}
}
