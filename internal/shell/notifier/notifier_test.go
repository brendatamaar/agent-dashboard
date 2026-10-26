package notifier

import (
	"context"
	"path/filepath"
	"testing"

	"agent-dashboard/internal/store"
)

func TestNotifierVAPIDKeysInitialization(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

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
