package api

import (
	"encoding/json"
	"net/http"

	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"
)

type SecretsHandler struct {
	store            *store.Store
	masterPassphrase string
}

func NewSecretsHandler(s *store.Store, masterPassphrase string) *SecretsHandler {
	return &SecretsHandler{
		store:            s,
		masterPassphrase: masterPassphrase,
	}
}

func (s *SecretsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSecrets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []store.Secret{}
	}
	writeJSON(w, http.StatusOK, list)
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

	// Derive master key from passphrase + user salt
	passphrase := s.masterPassphrase
	if passphrase == "" {
		passphrase = user.PasswordHash // fallback to secure hash
	}
	masterKey := crypto.DeriveKey(passphrase, user.MasterSalt)

	ciphertext, err := crypto.Encrypt([]byte(req.Value), masterKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encryption failed"})
		return
	}

	id := generateID("sec")
	if err := s.store.PutSecret(r.Context(), id, req.Kind, req.Name, ciphertext); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save secret: " + err.Error()})
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
