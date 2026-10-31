package crypto

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"agent-dashboard/internal/shell/logger"
	"agent-dashboard/internal/store"
)

// Vault keeps its independent encryption key outside SQLite. Backups must retain
// this protected file separately; missing keys never cause stored secrets to reset.
type Vault struct {
	store      *store.Store
	key        []byte
	passphrase string
	logger     *slog.Logger
	mu         sync.Mutex
}

const envelope = "agentd:v2:"

func NewVault(st *store.Store, passphrase string, log *slog.Logger) (*Vault, error) {
	var seq int
	var name, path string
	if err := st.DB().QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		return nil, fmt.Errorf("find secret key path: %w", err)
	}
	if path == "" {
		return nil, errors.New("encrypted secrets require a file-backed database")
	}
	keyPath := path + ".key"
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		// Creating a new key over encrypted v2 records would silently lose access.
		var count int
		if e := st.DB().QueryRow("SELECT COUNT(*) FROM secrets WHERE substr(ciphertext,1,10) = ?", []byte(envelope)).Scan(&count); e != nil {
			return nil, e
		}
		if count > 0 {
			return nil, errors.New("secret key file is missing; restore the original .key backup")
		}
		key = make([]byte, KeySize)
		if _, e := rand.Read(key); e != nil {
			return nil, e
		}
		if e := os.MkdirAll(filepath.Dir(keyPath), 0700); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(e) {
			key, err = os.ReadFile(keyPath)
		} else if e != nil {
			return nil, e
		} else {
			_, e = f.Write(key)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return nil, e
			}
			if closeErr != nil {
				return nil, closeErr
			}
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		return nil, errors.New("invalid secret key file")
	}
	if info, e := os.Stat(keyPath); e != nil {
		return nil, e
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret key file must be accessible only by its owner (chmod 600)")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Vault{store: st, key: key, passphrase: passphrase, logger: log}, nil
}

func (v *Vault) Put(ctx context.Context, kind, name, value string) error {
	ciphertext, err := Encrypt([]byte(value), v.key)
	if err != nil {
		return err
	}
	var id [12]byte
	if _, err = rand.Read(id[:]); err != nil {
		return err
	}
	if err = v.store.PutSecret(ctx, "sec_"+hex.EncodeToString(id[:]), kind, name, append([]byte(envelope), ciphertext...)); err != nil {
		return err
	}
	logger.RegisterSecrets(v.logger, value)
	return nil
}

func (v *Vault) Get(ctx context.Context, name string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	sec, err := v.store.GetSecret(ctx, name)
	if err != nil {
		return "", err
	}
	var plain []byte
	if bytes.HasPrefix(sec.Ciphertext, []byte(envelope)) {
		plain, err = Decrypt(sec.Ciphertext[len(envelope):], v.key)
	} else {
		// Legacy notification records were plaintext JSON. Encrypt them in place
		// only when valid, preserving the existing subscription identity/settings.
		if (sec.Kind == "vapid_keys" || sec.Kind == "notifier_settings") && json.Valid(sec.Ciphertext) {
			plain = sec.Ciphertext
		} else {
			if v.passphrase == "" {
				return "", errors.New("legacy encrypted secret requires its explicit master passphrase; restore/re-enter it before migration")
			}
			user, e := v.store.GetSetupUser(ctx)
			if e != nil {
				return "", e
			}
			plain, err = Decrypt(sec.Ciphertext, DeriveKey(v.passphrase, user.MasterSalt))
		}
		if err == nil {
			err = v.Put(ctx, sec.Kind, sec.Name, string(plain))
		}
	}
	if err != nil {
		return "", err
	}
	logger.RegisterSecrets(v.logger, string(plain))
	return string(plain), nil
}

func (v *Vault) DecryptSecret(ctx context.Context, name string) (string, error) {
	return v.Get(ctx, name)
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNotFound)
}
