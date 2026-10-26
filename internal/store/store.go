package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrUserExists     = errors.New("user already exists")
	ErrSessionExpired = errors.New("session has expired")
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Optimize SQLite for concurrency and reliability
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA synchronous = NORMAL;",
	}
	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

// User represents the administrative single-user.
type User struct {
	ID               string
	Username         string
	PasswordHash     string
	TOTPSecret       string
	MasterSalt       []byte
	IsSetupComplete  bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Passkey represents a registered WebAuthn credential.
type Passkey struct {
	ID              string
	UserID          string
	CredentialID    []byte
	PublicKey       []byte
	AttestationType string
	AAGUID          []byte
	SignCount       uint32
	CreatedAt       time.Time
}

// Session represents an authenticated user session.
type Session struct {
	ID        string
	TokenHash string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Secret represents an encrypted secret stored at rest.
type Secret struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Ciphertext []byte    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AuditEntry represents an entry in the audit log.
type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}

// HasCompletedSetup returns true if an admin user exists and setup is finished.
func (s *Store) HasCompletedSetup(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE is_setup_complete = 1").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetUserByUsername retrieves a user by username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, totp_secret, master_salt, is_setup_complete, created_at, updated_at
		FROM users WHERE username = ?
	`, username)

	var u User
	var isSetup int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.MasterSalt, &isSetup, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.IsSetupComplete = isSetup == 1
	return &u, nil
}

// GetUserByID retrieves a user by id.
func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, totp_secret, master_salt, is_setup_complete, created_at, updated_at
		FROM users WHERE id = ?
	`, id)

	var u User
	var isSetup int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.MasterSalt, &isSetup, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.IsSetupComplete = isSetup == 1
	return &u, nil
}

// CreateUser creates a new user.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	isSetup := 0
	if u.IsSetupComplete {
		isSetup = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, totp_secret, master_salt, is_setup_complete, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, u.ID, u.Username, u.PasswordHash, u.TOTPSecret, u.MasterSalt, isSetup)
	return err
}

// UpdateUser updates user credentials.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	isSetup := 0
	if u.IsSetupComplete {
		isSetup = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = ?, totp_secret = ?, is_setup_complete = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, u.PasswordHash, u.TOTPSecret, isSetup, u.ID)
	return err
}

// AddPasskey saves a WebAuthn passkey credential for a user.
func (s *Store) AddPasskey(ctx context.Context, p *Passkey) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, p.ID, p.UserID, p.CredentialID, p.PublicKey, p.AttestationType, p.AAGUID, p.SignCount)
	return err
}

// GetPasskeysForUser returns all passkeys registered to a user.
func (s *Store) GetPasskeysForUser(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, created_at
		FROM passkeys WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var passkeys []Passkey
	for rows.Next() {
		var p Passkey
		if err := rows.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.PublicKey, &p.AttestationType, &p.AAGUID, &p.SignCount, &p.CreatedAt); err != nil {
			return nil, err
		}
		passkeys = append(passkeys, p)
	}
	return passkeys, rows.Err()
}

// UpdatePasskeySignCount increments/updates the sign count of a passkey.
func (s *Store) UpdatePasskeySignCount(ctx context.Context, credentialID []byte, signCount uint32) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE passkeys SET sign_count = ? WHERE credential_id = ?
	`, signCount, credentialID)
	return err
}

// CreateSession generates a new authenticated session token and records it.
func (s *Store) CreateSession(ctx context.Context, userID string, duration time.Duration) (string, error) {
	rawBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawBytes); err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}
	token := hex.EncodeToString(rawBytes)
	hash := HashToken(token)

	sessionID := hex.EncodeToString(rawBytes[:16])
	expiresAt := time.Now().Add(duration)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, sessionID, hash, userID, expiresAt)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession verifies a token and returns the corresponding User.
func (s *Store) ValidateSession(ctx context.Context, token string) (*User, error) {
	hash := HashToken(token)
	var u User
	var isSetup int
	var expiresAt time.Time

	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.totp_secret, u.master_salt, u.is_setup_complete, u.created_at, u.updated_at, s.expires_at
		FROM sessions s
		JOIN users u ON s.user_id = u.id
		WHERE s.token_hash = ?
	`, hash).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.MasterSalt, &isSetup, &u.CreatedAt, &u.UpdatedAt, &expiresAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if time.Now().After(expiresAt) {
		_ = s.DeleteSession(ctx, token)
		return nil, ErrSessionExpired
	}

	u.IsSetupComplete = isSetup == 1
	return &u, nil
}

// DeleteSession deletes a session by token.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	hash := HashToken(token)
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash)
	return err
}

// DeleteExpiredSessions cleans up old sessions.
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP")
	return err
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PutSecret stores or updates an encrypted secret.
func (s *Store) PutSecret(ctx context.Context, id, kind, name string, ciphertext []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO secrets (id, kind, name, ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			kind = excluded.kind,
			ciphertext = excluded.ciphertext,
			updated_at = CURRENT_TIMESTAMP
	`, id, kind, name, ciphertext)
	return err
}

// GetSecret retrieves a secret by name.
func (s *Store) GetSecret(ctx context.Context, name string) (*Secret, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, kind, name, ciphertext, created_at, updated_at
		FROM secrets WHERE name = ?
	`, name)
	var sec Secret
	err := row.Scan(&sec.ID, &sec.Kind, &sec.Name, &sec.Ciphertext, &sec.CreatedAt, &sec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// ListSecrets retrieves metadata for all stored secrets (without ciphertext).
func (s *Store) ListSecrets(ctx context.Context) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, name, created_at, updated_at
		FROM secrets ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Secret
	for rows.Next() {
		var sec Secret
		if err := rows.Scan(&sec.ID, &sec.Kind, &sec.Name, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, sec)
	}
	return list, rows.Err()
}

// DeleteSecret removes a secret by name.
func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE name = ?", name)
	return err
}

// WriteAudit logs an action in the audit log.
func (s *Store) WriteAudit(ctx context.Context, actor, action, target, result string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (actor, action, target, result, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, actor, action, target, result)
	return err
}

// GetAuditLogs retrieves recent audit logs with pagination.
func (s *Store) GetAuditLogs(ctx context.Context, limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor, action, target, result, created_at
		FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Result, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}


// Repo represents a managed git repository.
type Repo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	RemoteURL     string    `json:"remote_url"`
	Path          string    `json:"path"`
	DefaultBranch string    `json:"default_branch"`
	SettingsJSON  string    `json:"settings_json"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CreateRepo saves a new repo record.
func (s *Store) CreateRepo(ctx context.Context, r *Repo) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repos (id, name, remote_url, path, default_branch, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, r.ID, r.Name, r.RemoteURL, r.Path, r.DefaultBranch, r.SettingsJSON)
	return err
}

// GetRepo retrieves a repo by ID.
func (s *Store) GetRepo(ctx context.Context, id string) (*Repo, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, remote_url, path, default_branch, settings_json, created_at, updated_at
		FROM repos WHERE id = ?
	`, id)
	var r Repo
	err := row.Scan(&r.ID, &r.Name, &r.RemoteURL, &r.Path, &r.DefaultBranch, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRepos returns all tracked repositories.
func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, remote_url, path, default_branch, settings_json, created_at, updated_at
		FROM repos ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.Name, &r.RemoteURL, &r.Path, &r.DefaultBranch, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// DeleteRepo removes a repo by ID.
func (s *Store) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE id = ?", id)
	return err
}

// AgentRecord represents a persisted agent session.
type AgentRecord struct {
	ID               string    `json:"id"`
	RepoID           string    `json:"repo_id"`
	Harness          string    `json:"harness"`
	Model            string    `json:"model"`
	Runtime          string    `json:"runtime"`
	Mode             string    `json:"mode"`
	Branch           string    `json:"branch"`
	WorktreePath     string    `json:"worktree_path"`
	TmuxSession      string    `json:"tmux_session"`
	ContainerID      string    `json:"container_id"`
	HarnessSessionID string    `json:"harness_session_id"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CreateAgent creates or updates an agent database record.
func (s *Store) CreateAgent(ctx context.Context, a *AgentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (id, repo_id, harness, model, runtime, mode, branch, worktree_path, tmux_session, container_id, harness_session_id, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			harness_session_id = excluded.harness_session_id,
			tmux_session = excluded.tmux_session,
			container_id = excluded.container_id,
			state = excluded.state,
			updated_at = CURRENT_TIMESTAMP
	`, a.ID, a.RepoID, a.Harness, a.Model, a.Runtime, a.Mode, a.Branch, a.WorktreePath, a.TmuxSession, a.ContainerID, a.HarnessSessionID, a.State)
	return err
}

// UpdateAgent updates an agent's state and session references.
func (s *Store) UpdateAgent(ctx context.Context, a *AgentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE agents SET
			harness_session_id = ?,
			tmux_session = ?,
			container_id = ?,
			state = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, a.HarnessSessionID, a.TmuxSession, a.ContainerID, a.State, a.ID)
	return err
}

// GetAgent retrieves an agent by ID.
func (s *Store) GetAgent(ctx context.Context, id string) (*AgentRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, repo_id, harness, model, runtime, mode, branch, worktree_path, tmux_session, container_id, harness_session_id, state, created_at, updated_at
		FROM agents WHERE id = ?
	`, id)
	var a AgentRecord
	err := row.Scan(&a.ID, &a.RepoID, &a.Harness, &a.Model, &a.Runtime, &a.Mode, &a.Branch, &a.WorktreePath, &a.TmuxSession, &a.ContainerID, &a.HarnessSessionID, &a.State, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAgents returns all agents, ordered by updated_at descending.
func (s *Store) ListAgents(ctx context.Context) ([]AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repo_id, harness, model, runtime, mode, branch, worktree_path, tmux_session, container_id, harness_session_id, state, created_at, updated_at
		FROM agents ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentRecord
	for rows.Next() {
		var a AgentRecord
		if err := rows.Scan(&a.ID, &a.RepoID, &a.Harness, &a.Model, &a.Runtime, &a.Mode, &a.Branch, &a.WorktreePath, &a.TmuxSession, &a.ContainerID, &a.HarnessSessionID, &a.State, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// DeleteAgent removes an agent by ID.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agents WHERE id = ?", id)
	return err
}

// EventRecord represents a stored event.
type EventRecord struct {
	ID          int64     `json:"id"`
	AgentID     string    `json:"agent_id"`
	Seq         int64     `json:"seq"`
	Type        string    `json:"type"`
	PayloadJSON string    `json:"payload_json"`
	CreatedAt   time.Time `json:"created_at"`
}

// AppendEvent stores a normalized event for an agent.
func (s *Store) AppendEvent(ctx context.Context, agentID string, seq int64, eventType, payloadJSON string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (agent_id, seq, type, payload_json, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, agentID, seq, eventType, payloadJSON)
	return err
}

// GetEvents retrieves all events for an agent with seq > afterSeq.
func (s *Store) GetEvents(ctx context.Context, agentID string, afterSeq int64) ([]EventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_id, seq, type, payload_json, created_at
		FROM events WHERE agent_id = ? AND seq > ?
		ORDER BY seq ASC
	`, agentID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EventRecord
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Seq, &e.Type, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
