package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"agent-dashboard/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
)

type NotificationPayload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	TargetURL string `json:"target_url,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Icon      string `json:"icon,omitempty"`
}

type Notifier struct {
	store      *store.Store
	logger     *slog.Logger
	publicKey  string
	privateKey string
	mu         sync.RWMutex
}

func NewNotifier(st *store.Store, logger *slog.Logger) (*Notifier, error) {
	if logger == nil {
		logger = slog.Default()
	}

	n := &Notifier{
		store:  st,
		logger: logger,
	}

	// Initialize VAPID keys from store or generate new ones
	if err := n.ensureVAPIDKeys(); err != nil {
		return nil, fmt.Errorf("failed to initialize VAPID keys: %w", err)
	}

	return n, nil
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

// Send sends a push notification to a specific subscription.
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

// NotifyAll dispatches the notification to all registered push subscribers.
func (n *Notifier) NotifyAll(ctx context.Context, payload NotificationPayload) {
	subs, err := n.store.ListPushSubscriptions(ctx)
	if err != nil {
		n.logger.Error("Failed to list push subscriptions", "error", err)
		return
	}

	for _, sub := range subs {
		if err := n.Send(ctx, sub, payload); err != nil {
			n.logger.Warn("Failed to send push notification", "endpoint", sub.Endpoint, "error", err)
		}
	}
}
