package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/core/scheduler"
)

var ErrConflict = errors.New("state version conflict")

// OutboxRecord is one ordered durable effect. Started non-idempotent effects
// have an unknown outcome after a crash and must never be blindly redelivered.
type OutboxRecord struct {
	ID                    int64
	Kind, Payload, Status string
}

func EncodeCommand(cmd agent.Command) (string, string, error) {
	b, err := json.Marshal(cmd)
	if err != nil {
		return "", "", err
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", cmd), "*"), string(b), nil
}
func DecodeCommand(kind, payload string) (agent.Command, error) {
	var cmd agent.Command
	switch kind {
	case "agent.CmdCreateWorktree":
		cmd = &agent.CmdCreateWorktree{}
	case "agent.CmdStartSession":
		cmd = &agent.CmdStartSession{}
	case "agent.CmdSendInput":
		cmd = &agent.CmdSendInput{}
	case "agent.CmdKillSession":
		cmd = &agent.CmdKillSession{}
	case "agent.CmdRemoveWorktree":
		cmd = &agent.CmdRemoveWorktree{}
	case "agent.CmdNotifyUser":
		cmd = &agent.CmdNotifyUser{}
	case "agent.CmdResolveApproval":
		cmd = &agent.CmdResolveApproval{}
	case "agent.CmdInterruptSession":
		cmd = &agent.CmdInterruptSession{}
	default:
		return nil, fmt.Errorf("unknown durable command %q", kind)
	}
	if err := json.Unmarshal([]byte(payload), cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (s *Store) LoadOrchestration(ctx context.Context) (agent.ManagerState, scheduler.State, int64, error) {
	var m agent.ManagerState
	var sc scheduler.State
	var version int64
	var mj, sj string
	err := s.db.QueryRowContext(ctx, "SELECT version,manager_json,scheduler_json FROM orchestration_state WHERE id=1").Scan(&version, &mj, &sj)
	if errors.Is(err, sql.ErrNoRows) {
		return m, sc, 0, ErrNotFound
	}
	if err != nil {
		return m, sc, 0, err
	}
	if err = json.Unmarshal([]byte(mj), &m); err != nil {
		return m, sc, 0, err
	}
	if err = json.Unmarshal([]byte(sj), &sc); err != nil {
		return m, sc, 0, err
	}
	if err = agent.ValidateManagerState(m); err != nil {
		return m, sc, 0, fmt.Errorf("invalid durable manager: %w", err)
	}
	return m, sc, version, nil
}

// CommitOrchestration atomically saves the full runnable state/FIFO, DB effects,
// and external effects. A failed expected-version check leaves no side effects.
func (s *Store) CommitOrchestration(ctx context.Context, expected int64, m agent.ManagerState, sc scheduler.State, cmds []agent.Command) ([]events.NormalizedEvent, error) {
	mj, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sj, err := json.Marshal(sc)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if expected == 0 {
		res, e := tx.ExecContext(ctx, "INSERT INTO orchestration_state(id,version,manager_json,scheduler_json) VALUES(1,1,?,?) ON CONFLICT(id) DO NOTHING", string(mj), string(sj))
		if e != nil {
			return nil, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, ErrConflict
		}
	} else {
		res, e := tx.ExecContext(ctx, "UPDATE orchestration_state SET version=version+1,manager_json=?,scheduler_json=? WHERE id=1 AND version=?", string(mj), string(sj), expected)
		if e != nil {
			return nil, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, ErrConflict
		}
	}
	// Projections exist before event/approval rows reference them.
	for _, a := range m.Agents {
		_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,repo_id,harness,model,runtime,mode,branch,worktree_path,tmux_session,harness_session_id,state,failure_reason,auto_approve,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET worktree_path=excluded.worktree_path,tmux_session=excluded.tmux_session,harness_session_id=excluded.harness_session_id,state=excluded.state,failure_reason=excluded.failure_reason,auto_approve=excluded.auto_approve,updated_at=excluded.updated_at`, a.ID, a.RepoID, a.Harness, a.Model, a.Runtime, a.Mode, a.Branch, a.WorktreePath, a.TmuxSession, a.HarnessSessionID, a.State, a.FailureReason, a.AutoApprove, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return nil, err
		}
	}
	var emitted []events.NormalizedEvent
	for _, cmd := range cmds {
		switch c := cmd.(type) {
		case agent.CmdPersistAgentState: // Included in the snapshot and projections above.
		case agent.CmdDeleteAgentRecord:
			_, err = tx.ExecContext(ctx, "DELETE FROM agents WHERE id=?", c.AgentID)
		case agent.CmdPersistEvent:
			ev := c.Event
			ev.AgentID = c.AgentID
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_event_cursors(agent_id,seq) VALUES(?,(SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE agent_id=?)) ON CONFLICT(agent_id) DO UPDATE SET seq=seq+1`, c.AgentID, c.AgentID)
			if err != nil {
				return nil, err
			}
			err = tx.QueryRowContext(ctx, "SELECT seq FROM agent_event_cursors WHERE agent_id=?", c.AgentID).Scan(&ev.Seq)
			if err != nil {
				return nil, err
			}
			payload, e := json.Marshal(ev.Payload)
			if e != nil {
				return nil, e
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO events(agent_id,seq,type,payload_json,created_at) VALUES(?,?,?,?,?)", c.AgentID, ev.Seq, ev.Type, string(payload), ev.Timestamp)
			if err == nil {
				_, err = tx.ExecContext(ctx, "DELETE FROM events WHERE agent_id=? AND seq <= ?", c.AgentID, ev.Seq-10000)
			}
			emitted = append(emitted, ev)
		case agent.CmdPersistApproval:
			_, err = tx.ExecContext(ctx, `INSERT INTO approvals(id,agent_id,request_json,decision,decided_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET decision=excluded.decision,decided_at=excluded.decided_at WHERE approvals.agent_id=excluded.agent_id`, c.ID, c.AgentID, c.Request, c.Decision, c.DecidedAt)
		case agent.CmdAuditLog:
			_, err = tx.ExecContext(ctx, "INSERT INTO audit_log(actor,action,target,result) VALUES(?,?,?,?)", s.RedactAudit(c.Actor), s.RedactAudit(c.Action), s.RedactAudit(c.Target), s.RedactAudit(c.Result))
		default:
			var kind, payload string
			kind, payload, err = EncodeCommand(cmd)
			if err == nil {
				_, err = tx.ExecContext(ctx, "INSERT INTO orchestration_outbox(kind,payload_json) VALUES(?,?)", kind, payload)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	// Schedules and their reservation map are committed with the manager.
	for _, item := range sc.Schedules {
		_, err = tx.ExecContext(ctx, "UPDATE schedules SET last_run_at=?,next_run_at=? WHERE id=?", item.LastRunAt, item.NextRunAt, item.ID)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return emitted, nil
}
func (s *Store) NextOutbox(ctx context.Context) (*OutboxRecord, error) {
	r := new(OutboxRecord)
	err := s.db.QueryRowContext(ctx, "SELECT id,kind,payload_json,status FROM orchestration_outbox WHERE status!='done' ORDER BY id LIMIT 1").Scan(&r.ID, &r.Kind, &r.Payload, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}
func (s *Store) StartOutbox(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE orchestration_outbox SET status='started' WHERE id=?", id)
	return err
}
func (s *Store) CompleteOutbox(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "UPDATE orchestration_outbox SET status='done' WHERE id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM orchestration_outbox WHERE status='done' AND id<?", id-10000); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SaveHookToken(ctx context.Context, id, hash string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO agent_hook_tokens(agent_id,token_hash) VALUES(?,?) ON CONFLICT(agent_id) DO UPDATE SET token_hash=excluded.token_hash", id, hash)
	return err
}
func (s *Store) HookTokenHash(ctx context.Context, id string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT token_hash FROM agent_hook_tokens WHERE agent_id=?", id).Scan(&hash)
	return hash, err
}

// CreateInitialUser is a single SQLite statement, so competing setup requests
// cannot create two administrators even if they use different usernames.
func (s *Store) CreateInitialUser(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,totp_secret,master_salt,is_setup_complete) SELECT ?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM users)`, u.ID, u.Username, u.PasswordHash, u.TOTPSecret, u.MasterSalt, u.IsSetupComplete)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrUserExists
	}
	return nil
}
func (s *Store) GetSetupUser(ctx context.Context) (*User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM users ORDER BY created_at,id LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}
func (s *Store) EventsPage(ctx context.Context, id string, after int64, limit int) ([]EventRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if after < 0 {
		after = 0
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,agent_id,seq,type,payload_json,created_at FROM events WHERE agent_id=? AND seq>? ORDER BY seq,id LIMIT ?", id, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]EventRecord, 0)
	for rows.Next() {
		var ev EventRecord
		if err = rows.Scan(&ev.ID, &ev.AgentID, &ev.Seq, &ev.Type, &ev.PayloadJSON, &ev.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, ev)
	}
	return result, rows.Err()
}

// HasPendingSessionStart distinguishes an absent runtime from a committed
// startup command whose create effect has not yet run after a restart.
func (s *Store) HasPendingSessionStart(ctx context.Context, id, handle string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orchestration_outbox WHERE kind='agent.CmdStartSession' AND status!='done' AND json_extract(payload_json,'$.AgentID')=? AND json_extract(payload_json,'$.TmuxSession')=?)`, id, handle).Scan(&exists)
	return exists, err
}
