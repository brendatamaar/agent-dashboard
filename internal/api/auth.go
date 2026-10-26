package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent-dashboard/internal/shell/crypto"
	"agent-dashboard/internal/store"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	store       *store.Store
	cfg         Config
	limiter     *RateLimiterMiddleware
	webauthn    *webauthn.WebAuthn
	sessionMu   sync.Mutex
	waSessions  map[string]*webauthn.SessionData
}

func NewAuthHandler(s *store.Store, cfg Config, limiter *RateLimiterMiddleware) (*AuthHandler, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "agent-dashboard",
		RPID:          cfg.RPID,
		RPOrigins:     []string{cfg.RPOrigin},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize webauthn: %w", err)
	}

	return &AuthHandler{
		store:      s,
		cfg:        cfg,
		limiter:    limiter,
		webauthn:   wa,
		waSessions: make(map[string]*webauthn.SessionData),
	}, nil
}

type SetupStatusResponse struct {
	SetupNeeded bool `json:"setup_needed"`
}

func (a *AuthHandler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	hasSetup, err := a.store.HasCompletedSetup(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, SetupStatusResponse{SetupNeeded: !hasSetup})
}

type SetupInitRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SetupInitResponse struct {
	TOTPSecret string `json:"totp_secret"`
	TOTPURL    string `json:"totp_url"`
}

func (a *AuthHandler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	hasSetup, err := a.store.HasCompletedSetup(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if hasSetup {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "setup already completed"})
		return
	}

	var req SetupInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if len(req.Username) < 3 || len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username (min 3 chars) and password (min 8 chars) required"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		return
	}

	totpKey, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "agent-dashboard",
		AccountName: req.Username,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate TOTP"})
		return
	}

	salt, err := crypto.GenerateSalt()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate salt"})
		return
	}

	user := &store.User{
		ID:              generateID("usr"),
		Username:        req.Username,
		PasswordHash:    string(hash),
		TOTPSecret:      totpKey.Secret(),
		MasterSalt:      salt,
		IsSetupComplete: true,
	}

	if err := a.store.CreateUser(r.Context(), user); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create user: " + err.Error()})
		return
	}

	_ = a.store.WriteAudit(r.Context(), req.Username, "setup", "user", "success")

	// Create initial session for seamless onboarding
	token, err := a.store.CreateSession(r.Context(), user.ID, a.cfg.SessionDuration)
	if err == nil {
		a.setSessionCookie(w, token)
	}

	writeJSON(w, http.StatusOK, SetupInitResponse{
		TOTPSecret: totpKey.Secret(),
		TOTPURL:    totpKey.URL(),
	})
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

func (a *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	user, err := a.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !user.IsSetupComplete {
		a.limiter.RecordAttempt(ip, false)
		_ = a.store.WriteAudit(r.Context(), req.Username, "login", "password", "failed_user_not_found")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username, password, or TOTP code"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		a.limiter.RecordAttempt(ip, false)
		_ = a.store.WriteAudit(r.Context(), req.Username, "login", "password", "failed_bad_password")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username, password, or TOTP code"})
		return
	}

	// Verify TOTP if configured
	if user.TOTPSecret != "" {
		if !totp.Validate(req.TOTPCode, user.TOTPSecret) {
			a.limiter.RecordAttempt(ip, false)
			_ = a.store.WriteAudit(r.Context(), req.Username, "login", "totp", "failed_bad_totp")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username, password, or TOTP code"})
			return
		}
	}

	a.limiter.RecordAttempt(ip, true)
	token, err := a.store.CreateSession(r.Context(), user.ID, a.cfg.SessionDuration)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}

	a.setSessionCookie(w, token)
	_ = a.store.WriteAudit(r.Context(), req.Username, "login", "session", "success")

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "authenticated",
		"username": user.Username,
	})
}

func (a *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("agentd_session")
	if err == nil && cookie.Value != "" {
		_ = a.store.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "agentd_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.cfg.RPOrigin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (a *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	passkeys, _ := a.store.GetPasskeysForUser(r.Context(), user.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":             user.ID,
		"username":       user.Username,
		"has_totp":       user.TOTPSecret != "",
		"passkeys_count": len(passkeys),
	})
}

// WebAuthn Passkey Registration & Login

type waUser struct {
	user  *store.User
	creds []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return []byte(u.user.ID) }
func (u *waUser) WebAuthnName() string                       { return u.user.Username }
func (u *waUser) WebAuthnDisplayName() string                { return u.user.Username }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (a *AuthHandler) getWebAuthnUser(r *http.Request, u *store.User) (*waUser, error) {
	pks, err := a.store.GetPasskeysForUser(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	var creds []webauthn.Credential
	for _, p := range pks {
		creds = append(creds, webauthn.Credential{
			ID:              p.CredentialID,
			PublicKey:       p.PublicKey,
			AttestationType: p.AttestationType,
			Authenticator: webauthn.Authenticator{
				AAGUID:    p.AAGUID,
				SignCount: p.SignCount,
			},
		})
	}
	return &waUser{user: u, creds: creds}, nil
}

func (a *AuthHandler) HandlePasskeyBeginRegistration(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	wau, err := a.getWebAuthnUser(r, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	options, sessionData, err := a.webauthn.BeginRegistration(wau)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	sessionKey := generateID("wa_reg")
	a.sessionMu.Lock()
	a.waSessions[sessionKey] = sessionData
	a.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "agentd_wa_session",
		Value:    sessionKey,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.cfg.RPOrigin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, options)
}

func (a *AuthHandler) HandlePasskeyFinishRegistration(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	cookie, err := r.Cookie("agentd_wa_session")
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing registration session"})
		return
	}

	a.sessionMu.Lock()
	sessionData, exists := a.waSessions[cookie.Value]
	delete(a.waSessions, cookie.Value)
	a.sessionMu.Unlock()

	if !exists {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expired registration session"})
		return
	}

	wau, err := a.getWebAuthnUser(r, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	credential, err := a.webauthn.FinishRegistration(wau, *sessionData, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "passkey registration failed: " + err.Error()})
		return
	}

	passkey := &store.Passkey{
		ID:              generateID("pk"),
		UserID:          user.ID,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
	}

	if err := a.store.AddPasskey(r.Context(), passkey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save passkey: " + err.Error()})
		return
	}

	_ = a.store.WriteAudit(r.Context(), user.Username, "passkey_register", hex.EncodeToString(credential.ID), "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "passkey_registered"})
}

func (a *AuthHandler) HandlePasskeyBeginLogin(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username == "" {
		// Single-user default: find the first registered user
		row := a.store.DB().QueryRowContext(r.Context(), "SELECT username FROM users WHERE is_setup_complete = 1 LIMIT 1")
		_ = row.Scan(&username)
	}

	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no user configured"})
		return
	}

	user, err := a.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	wau, err := a.getWebAuthnUser(r, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	options, sessionData, err := a.webauthn.BeginLogin(wau)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	sessionKey := generateID("wa_login")
	a.sessionMu.Lock()
	a.waSessions[sessionKey] = sessionData
	a.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "agentd_wa_login_session",
		Value:    sessionKey + ":" + user.ID,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.cfg.RPOrigin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, options)
}

func (a *AuthHandler) HandlePasskeyFinishLogin(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("agentd_wa_login_session")
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing login session"})
		return
	}

	var sessionKey, userID string
	parts := strings.Split(cookie.Value, ":")
	if len(parts) == 2 {
		sessionKey = parts[0]
		userID = parts[1]
	} else {
		sessionKey = cookie.Value
	}

	a.sessionMu.Lock()
	sessionData, exists := a.waSessions[sessionKey]
	delete(a.waSessions, sessionKey)
	a.sessionMu.Unlock()

	if !exists {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expired login session"})
		return
	}

	user, err := a.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	wau, err := a.getWebAuthnUser(r, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	credential, err := a.webauthn.FinishLogin(wau, *sessionData, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "passkey verification failed: " + err.Error()})
		return
	}

	_ = a.store.UpdatePasskeySignCount(r.Context(), credential.ID, credential.Authenticator.SignCount)

	token, err := a.store.CreateSession(r.Context(), user.ID, a.cfg.SessionDuration)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}

	a.setSessionCookie(w, token)
	_ = a.store.WriteAudit(r.Context(), user.Username, "login", "passkey", "success")

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "authenticated",
		"username": user.Username,
	})
}

func (a *AuthHandler) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "agentd_session",
		Value:    token,
		Path:     "/",
		MaxAge:   int(a.cfg.SessionDuration.Seconds()),
		Expires:  time.Now().Add(a.cfg.SessionDuration),
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.cfg.RPOrigin, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	_, _ = io.ReadFull(rand.Reader, b)
	return prefix + "_" + hex.EncodeToString(b)
}
