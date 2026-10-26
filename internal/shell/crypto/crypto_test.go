package crypto_test

import (
	"bytes"
	"testing"

	"agent-dashboard/internal/shell/crypto"
)

func TestEncryptDecrypt(t *testing.T) {
	passphrase := "super-secure-master-passphrase-2026"
	salt, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatalf("failed to generate salt: %v", err)
	}

	key := crypto.DeriveKey(passphrase, salt)
	if len(key) != crypto.KeySize {
		t.Fatalf("expected key size %d, got %d", crypto.KeySize, len(key))
	}

	plaintext := []byte("github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOP")

	ciphertext, err := crypto.Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	decrypted, err := crypto.Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypted = %q, want %q", decrypted, plaintext)
	}

	// Tampered ciphertext fails
	ciphertext[len(ciphertext)-1] ^= 0xFF
	_, err = crypto.Decrypt(ciphertext, key)
	if err == nil {
		t.Fatal("expected decryption failure for tampered ciphertext")
	}

	// Wrong key fails
	wrongKey := crypto.DeriveKey("wrong-passphrase", salt)
	_, err = crypto.Decrypt(ciphertext, wrongKey)
	if err == nil {
		t.Fatal("expected decryption failure for wrong key")
	}
}
