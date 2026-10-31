package crypto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-dashboard/internal/store"
)

func vaultStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}
func TestVaultIndependentKeyAndRestart(t *testing.T) {
	st, path := vaultStore(t)
	ctx := context.Background()
	vault, err := NewVault(st, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.Put(ctx, "github", "token", "custom-secret-canary"); err != nil {
		t.Fatal(err)
	}
	sec, err := st.GetSecret(ctx, "token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sec.Ciphertext, []byte("custom-secret-canary")) {
		t.Fatal("stored plaintext")
	}
	if !bytes.HasPrefix(sec.Ciphertext, []byte(envelope)) {
		t.Fatal("missing encryption envelope")
	}
	if key, err := os.ReadFile(path + ".key"); err != nil || len(key) != KeySize {
		t.Fatalf("missing independent key: %v", err)
	}
	restarted, err := NewVault(st, "different-password", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Get(ctx, "token"); err != nil || got != "custom-secret-canary" {
		t.Fatalf("restart/password change lost secret: %q %v", got, err)
	}
	if err = os.Remove(path + ".key"); err != nil {
		t.Fatal(err)
	}
	if _, err = NewVault(st, "", nil); err == nil {
		t.Fatal("missing key silently replaced for existing ciphertext")
	}
}
func TestVaultLegacyMigrationPreservesDataAndRequiresExplicitPassphrase(t *testing.T) {
	st, _ := vaultStore(t)
	ctx := context.Background()
	salt := []byte("0123456789abcdef")
	user := &store.User{ID: "u", Username: "alice", PasswordHash: "database-known-hash", MasterSalt: salt, IsSetupComplete: true}
	if err := st.CreateInitialUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	old, err := Encrypt([]byte("legacy-canary"), DeriveKey("explicit-legacy-passphrase", salt))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.PutSecret(ctx, "legacy", "github", "github_token", old); err != nil {
		t.Fatal(err)
	}
	weak, err := Encrypt([]byte("database-derived-canary"), DeriveKey(user.PasswordHash, salt))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.PutSecret(ctx, "weak", "github", "weak_token", weak); err != nil {
		t.Fatal(err)
	}
	locked, err := NewVault(st, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locked.Get(ctx, "github_token"); err == nil {
		t.Fatal("legacy decryption used database-derived fallback")
	}
	if _, err = locked.Get(ctx, "weak_token"); err == nil {
		t.Fatal("database-known password hash decrypted a legacy secret without explicit authorization")
	}
	sec, _ := st.GetSecret(ctx, "github_token")
	if !bytes.Equal(sec.Ciphertext, old) {
		t.Fatal("failed migration modified existing secret")
	}
	unlocked, err := NewVault(st, "explicit-legacy-passphrase", nil)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := unlocked.Get(ctx, "github_token"); err != nil || value != "legacy-canary" {
		t.Fatalf("legacy migration failed: %q %v", value, err)
	}
	sec, _ = st.GetSecret(ctx, "github_token")
	if !bytes.HasPrefix(sec.Ciphertext, []byte(envelope)) {
		t.Fatal("legacy record not reencrypted")
	}
}
