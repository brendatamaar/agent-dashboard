package api

import (
	"context"
	"encoding/json"
	"net/http"

	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"
)

type SecretsHandler struct {
	store   *store.Store
	vault   *crypto.Vault
	initErr error
}

func NewSecretsHandler(s *store.Store, masterPassphrase string) *SecretsHandler {
	vault, err := crypto.NewVault(s, masterPassphrase, nil)
	return &SecretsHandler{store: s, vault: vault, initErr: err}
}

func (s *SecretsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSecrets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var filtered []store.Secret
	for _, sec := range list {
		if sec.Kind != "vapid_keys" && sec.Kind != "notifier_settings" {
			filtered = append(filtered, sec)
		}
	}
	if filtered == nil {
		filtered = []store.Secret{}
	}
	writeJSON(w, http.StatusOK, filtered)
}

type CreateSecretRequest struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (s *SecretsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req CreateSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Name == "" || req.Value == "" || req.Kind == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, kind, and value are required"})
		return
	}

	if s.initErr != nil {
		writeError(w, http.StatusInternalServerError, "secret vault unavailable")
		return
	}
	if err := s.vault.Put(r.Context(), req.Kind, req.Name, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save secret")
		return
	}

	_ = s.store.WriteAudit(r.Context(), user.Username, "secret_put", req.Name, "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "name": req.Name})
}

func (s *SecretsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "secret name required"})
		return
	}

	if err := s.store.DeleteSecret(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete secret: " + err.Error()})
		return
	}

	_ = s.store.WriteAudit(r.Context(), user.Username, "secret_delete", name, "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

// DecryptSecret retrieves and decrypts a secret by name.
func (s *SecretsHandler) DecryptSecret(ctx context.Context, name string) (string, error) {
	if s.initErr != nil {
		return "", s.initErr
	}
	return s.vault.Get(ctx, name)
}
