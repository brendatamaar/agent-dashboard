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
}

type Agent struct {
	ID               string
	RepoID           string
	RepoPath         string
	Harness          string
	Model            string
	Runtime          string
	Mode             string
	Prompt           string
	Branch           string
	BaseBranch       string
	WorktreePath     string
	TmuxSession      string
	HarnessSessionID string
	State            State
	ExitCode         *int
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
	Agents             map[string]Agent
	Queue              []string                    // FIFO queue of agent IDs in StateQueued
	AlwaysAllowedTools map[string]map[string]bool // agentID -> toolName -> true
	PendingApprovals   map[string]ApprovalRequest // approvalID -> request
}

func NewManagerState(runningCap int) ManagerState {
	if runningCap <= 0 {
		runningCap = 1
	}
	if runningCap > 2 {
		runningCap = 2
	}
	return ManagerState{
		RunningCap:         runningCap,
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

func (s ManagerState) Clone() ManagerState {
	cloned := ManagerState{
		RunningCap:         s.RunningCap,
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
