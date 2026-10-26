package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"agent-dashboard/internal/shell/notifier"
	"agent-dashboard/internal/store"
)

type PushHandler struct {
	store    *store.Store
	notifier *notifier.Notifier
}

func NewPushHandler(st *store.Store, n *notifier.Notifier) *PushHandler {
	return &PushHandler{
		store:    st,
		notifier: n,
	}
}

// HandleGetVAPIDKey returns the public VAPID key for web push subscriptions.
func (h *PushHandler) HandleGetVAPIDKey(w http.ResponseWriter, r *http.Request) {
	if h.notifier == nil {
		writeError(w, http.StatusServiceUnavailable, "push notifier is not configured")
		return
	}
	pub := h.notifier.VAPIDPublicKey()
	writeJSON(w, http.StatusOK, map[string]string{"public_key": pub})
}

type PushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// HandleSubscribe registers a browser's Web Push subscription.
func (h *PushHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	var req PushSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeError(w, http.StatusBadRequest, "endpoint and keys (p256dh, auth) are required")
		return
	}

	if err := h.store.SavePushSubscription(r.Context(), req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save subscription: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "subscribed"})
}

type PushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// HandleUnsubscribe removes a registered Web Push subscription.
func (h *PushHandler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req PushUnsubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint is required")
		return
	}

	if err := h.store.DeletePushSubscription(r.Context(), req.Endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete subscription: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

// HandleTestPush sends a test push notification to all subscribed devices.
func (h *PushHandler) HandleTestPush(w http.ResponseWriter, r *http.Request) {
	if h.notifier == nil {
		writeError(w, http.StatusServiceUnavailable, "push notifier is not configured")
		return
	}

	h.notifier.NotifyAll(r.Context(), notifier.NotificationPayload{
		Title:     "agent-dashboard test",
		Body:      "Push notifications are working correctly!",
		TargetURL: "/",
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
