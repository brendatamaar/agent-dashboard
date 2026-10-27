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
}

type Agent struct {
	ID               string    `json:"id"`
	RepoID           string    `json:"repo_id"`
	RepoPath         string    `json:"repo_path"`
	Harness          string    `json:"harness"`
	Model            string    `json:"model"`
	Runtime          string    `json:"runtime"`
	Mode             string    `json:"mode"`
	Prompt           string    `json:"prompt"`
	Branch           string    `json:"branch"`
	BaseBranch       string    `json:"base_branch"`
	WorktreePath     string    `json:"worktree_path"`
	TmuxSession      string    `json:"tmux_session"`
	HarnessSessionID string    `json:"harness_session_id"`
	State            State     `json:"state"`
	ExitCode         *int      `json:"exit_code,omitempty"`
	FailureReason    string    `json:"failure_reason,omitempty"`
	AutoApprove      bool      `json:"auto_approve"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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
	RunningCap         int
	RepoCap            int                         // max concurrent running agents per repository (default 1)
	Agents             map[string]Agent
	Queue              []string                    // FIFO queue of agent IDs in StateQueued
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
		RunningCap:         runningCap,
		RepoCap:            rc,
		Agents:             make(map[string]Agent),
		Queue:              make([]string, 0),
		AlwaysAllowedTools: make(map[string]map[string]bool),
		PendingApprovals:   make(map[string]ApprovalRequest),
	}
}

func (s ManagerState) ActiveCount() int {
	count := 0
	for _, a := range s.Agents {
		if IsActive(a.State) {
			count++
		}
	}
	return count
}

func (s ManagerState) ActiveCountForRepo(repoID string) int {
	count := 0
	for _, a := range s.Agents {
		if a.RepoID == repoID && IsActive(a.State) {
			count++
		}
	}
	return count
}

func (s ManagerState) Clone() ManagerState {
	cloned := ManagerState{
		RunningCap:         s.RunningCap,
		RepoCap:            s.RepoCap,
		Agents:             make(map[string]Agent, len(s.Agents)),
		Queue:              make([]string, len(s.Queue)),
		AlwaysAllowedTools: make(map[string]map[string]bool, len(s.AlwaysAllowedTools)),
		PendingApprovals:   make(map[string]ApprovalRequest, len(s.PendingApprovals)),
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
