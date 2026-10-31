package agent

import (
	"fmt"
	"time"
)

type State string

const (
	StateQueued           State = "queued"
	StateProvisioning     State = "provisioning"
	StateRunning          State = "running"
	StateAwaitingApproval State = "awaiting_approval"
	StateIdle             State = "idle"
	StateStopped          State = "stopped"
	StateDone             State = "done"
	StateFailed           State = "failed"
)

func IsActive(s State) bool {
	switch s {
	case StateProvisioning, StateRunning, StateAwaitingApproval, StateIdle:
		return true
	default:
		return false
	}
}

func IsTerminal(s State) bool {
	switch s {
	case StateStopped, StateDone, StateFailed:
		return true
	default:
		return false
	}
}

func ValidateStateTransition(from, to State) error {
	if from == to {
		return nil
	}
	switch from {
	case StateQueued:
		if to == StateProvisioning || to == StateStopped {
			return nil
		}
	case StateProvisioning:
		if to == StateRunning || to == StateFailed || to == StateStopped {
			return nil
		}
	case StateRunning:
		if to == StateAwaitingApproval || to == StateIdle || to == StateDone || to == StateFailed || to == StateStopped {
			return nil
		}
	case StateAwaitingApproval:
		if to == StateRunning || to == StateFailed || to == StateStopped {
			return nil
		}
	case StateIdle:
		if to == StateRunning || to == StateDone || to == StateFailed || to == StateStopped {
			return nil
		}
	case StateStopped, StateDone, StateFailed:
		// Terminal states cannot transition to other states except via an explicit resume (which creates or restarts)
		return fmt.Errorf("illegal transition from terminal state %q to %q", from, to)
	}
	return fmt.Errorf("illegal transition from %q to %q", from, to)
}

type AgentSpec struct {
	ID           string
	RepoID       string
	RepoPath     string
	Harness      string
	Model        string
	Runtime      string
	Mode         string // "task" | "interactive"
	Prompt       string
	Branch       string
	BaseBranch   string
	WorktreePath string
	TmuxSession  string
	AutoApprove  bool
	Image        string
	SetupCommand string
	EnvVars      map[string]string
	AllowedTools []string
}

type Agent struct {
	Image            string            `json:"image,omitempty"`
	SetupCommand     string            `json:"setup_command,omitempty"`
	EnvVars          map[string]string `json:"env_vars,omitempty"`
	AllowedTools     []string          `json:"allowed_tools,omitempty"`
	OutputCursor     int64             `json:"output_cursor,omitempty"`
	Resuming         bool              `json:"resuming,omitempty"`
	ID               string            `json:"id"`
	RepoID           string            `json:"repo_id"`
	RepoPath         string            `json:"repo_path"`
	Harness          string            `json:"harness"`
	Model            string            `json:"model"`
	Runtime          string            `json:"runtime"`
	Mode             string            `json:"mode"`
	Prompt           string            `json:"prompt"`
	Branch           string            `json:"branch"`
	BaseBranch       string            `json:"base_branch"`
	WorktreePath     string            `json:"worktree_path"`
	TmuxSession      string            `json:"tmux_session"`
	HarnessSessionID string            `json:"harness_session_id"`
	State            State             `json:"state"`
	ExitCode         *int              `json:"exit_code,omitempty"`
	FailureReason    string            `json:"failure_reason,omitempty"`
	AutoApprove      bool              `json:"auto_approve"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type ApprovalRequest struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input,omitempty"`
	Command   string         `json:"command,omitempty"`
	FilePath  string         `json:"file_path,omitempty"`
	Diff      string         `json:"diff,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ManagerState struct {
	Stopping           map[string]Agent // Retain physical slots until destruction is acknowledged.
	Deleting           map[string]bool
	RunningCap         int
	RepoCap            int // max concurrent running agents per repository (default 1)
	Agents             map[string]Agent
	Queue              []string                   // FIFO queue of agent IDs in StateQueued
	AlwaysAllowedTools map[string]map[string]bool // agentID -> toolName -> true
	PendingApprovals   map[string]ApprovalRequest // approvalID -> request
}

func NewManagerState(runningCap int, repoCap ...int) ManagerState {
	if runningCap <= 0 {
		runningCap = 1
	}
	if runningCap > 2 {
		runningCap = 2
	}
	rc := 1
	if len(repoCap) > 0 && repoCap[0] > 0 {
		rc = repoCap[0]
	}
	return ManagerState{
		Stopping:           make(map[string]Agent),
		Deleting:           make(map[string]bool),
		RunningCap:         runningCap,
		RepoCap:            rc,
		Agents:             make(map[string]Agent),
		Queue:              make([]string, 0),
		AlwaysAllowedTools: make(map[string]map[string]bool),
		PendingApprovals:   make(map[string]ApprovalRequest),
	}
}

func (s ManagerState) ActiveCount() int {
	count := len(s.Stopping)
	for id, a := range s.Agents {
		if _, stopping := s.Stopping[id]; IsActive(a.State) && !stopping {
			count++
		}
	}
	return count
}

func (s ManagerState) ActiveCountForRepo(repoID string) int {
	count := 0
	for _, a := range s.Stopping {
		if a.RepoID == repoID {
			count++
		}
	}
	for _, a := range s.Agents {
		if _, stopping := s.Stopping[a.ID]; a.RepoID == repoID && IsActive(a.State) && !stopping {
			count++
		}
	}
	return count
}

func (s ManagerState) Clone() ManagerState {
	cloned := ManagerState{
		Stopping:           make(map[string]Agent, len(s.Stopping)),
		Deleting:           make(map[string]bool, len(s.Deleting)),
		RunningCap:         s.RunningCap,
		RepoCap:            s.RepoCap,
		Agents:             make(map[string]Agent, len(s.Agents)),
		Queue:              make([]string, len(s.Queue)),
		AlwaysAllowedTools: make(map[string]map[string]bool, len(s.AlwaysAllowedTools)),
		PendingApprovals:   make(map[string]ApprovalRequest, len(s.PendingApprovals)),
	}
	for k, v := range s.Stopping {
		cloned.Stopping[k] = v
	}
	for k, v := range s.Deleting {
		cloned.Deleting[k] = v
	}
	for k, v := range s.Agents {
		cloned.Agents[k] = v
	}
	copy(cloned.Queue, s.Queue)
	for aID, tools := range s.AlwaysAllowedTools {
		toolMap := make(map[string]bool, len(tools))
		for tName, val := range tools {
			toolMap[tName] = val
		}
		cloned.AlwaysAllowedTools[aID] = toolMap
	}
	for appID, req := range s.PendingApprovals {
		cloned.PendingApprovals[appID] = req
	}
	return cloned
}

// ValidateManagerState checks a recovered snapshot before it can issue effects.
func ValidateManagerState(s ManagerState) error {
	if s.RunningCap < 1 || s.RunningCap > 2 || s.RepoCap < 1 {
		return fmt.Errorf("invalid persisted capacity")
	}
	queued := make(map[string]bool, len(s.Queue))
	for _, id := range s.Queue {
		a, ok := s.Agents[id]
		if !ok || a.State != StateQueued || queued[id] {
			return fmt.Errorf("invalid persisted queue entry %q", id)
		}
		queued[id] = true
	}
	for id, a := range s.Agents {
		if id == "" || a.ID != id {
			return fmt.Errorf("invalid persisted agent identity")
		}
		switch a.State {
		case StateQueued:
			if !queued[id] {
				return fmt.Errorf("queued agent %q absent from FIFO", id)
			}
		case StateProvisioning, StateRunning, StateAwaitingApproval, StateIdle, StateStopped, StateDone, StateFailed:
		default:
			return fmt.Errorf("invalid persisted agent state %q", a.State)
		}
	}
	for id, req := range s.PendingApprovals {
		a, ok := s.Agents[req.AgentID]
		if id == "" || id != req.ID || !ok || a.State != StateAwaitingApproval {
			return fmt.Errorf("invalid persisted approval owner %q", id)
		}
	}
	for id, stopping := range s.Stopping {
		a, ok := s.Agents[id]
		if !ok || stopping.ID != id || !IsTerminal(a.State) {
			return fmt.Errorf("invalid persisted stopping agent %q", id)
		}
	}
	for _, a := range s.Agents {
		if s.ActiveCountForRepo(a.RepoID) > s.RepoCap {
			return fmt.Errorf("persisted repository capacity exceeds limit")
		}
	}
	if s.ActiveCount() > s.RunningCap {
		return fmt.Errorf("persisted physical capacity exceeds limit")
	}
	return nil
}
