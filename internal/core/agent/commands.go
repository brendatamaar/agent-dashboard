package agent

import (
	"time"

	"agent-dashboard/internal/core/events"
)

// Input represents events entering the pure agent manager.
type Input interface {
	isInput()
}

type InputSubmitAgent struct {
	Now  time.Time
	Spec AgentSpec
}

func (InputSubmitAgent) isInput() {}

type InputWorktreeReady struct {
	Now          time.Time
	AgentID      string
	WorktreePath string
}

func (InputWorktreeReady) isInput() {}

type InputWorktreeFailed struct {
	Now     time.Time
	AgentID string
	Error   string
}

func (InputWorktreeFailed) isInput() {}

type InputSessionStarted struct {
	Now              time.Time
	AgentID          string
	TmuxSession      string
	HarnessSessionID string
}

func (InputSessionStarted) isInput() {}

type InputHarnessEvent struct {
	Now     time.Time
	AgentID string
	Event   events.NormalizedEvent
}

func (InputHarnessEvent) isInput() {}

type InputSendMessage struct {
	Now     time.Time
	AgentID string
	Message string
}

func (InputSendMessage) isInput() {}

type InputApprovalDecision struct {
	Now        time.Time
	AgentID    string
	ApprovalID string
	Approved   bool
}

func (InputApprovalDecision) isInput() {}

type InputProcessExited struct {
	Now      time.Time
	AgentID  string
	ExitCode int
}

func (InputProcessExited) isInput() {}

type InputStopRequested struct {
	Now     time.Time
	AgentID string
}

func (InputStopRequested) isInput() {}

type InputDeleteRequested struct {
	Now     time.Time
	AgentID string
}

func (InputDeleteRequested) isInput() {}

type InputReconcileBoot struct {
	Now                 time.Time
	RunningTmuxSessions map[string]bool // set of active tmux session names
}

func (InputReconcileBoot) isInput() {}

// Command represents an effect for the imperative shell to execute.
type Command interface {
	isCommand()
}

type CmdCreateWorktree struct {
	AgentID      string
	RepoPath     string
	BaseBranch   string
	Branch       string
	WorktreePath string
}

func (CmdCreateWorktree) isCommand() {}

type CmdStartSession struct {
	AgentID      string
	WorktreePath string
	TmuxSession  string
	Harness      string
	Model        string
	Mode         string
	Prompt       string
	ResumeID     string
}

func (CmdStartSession) isCommand() {}

type CmdSendInput struct {
	AgentID     string
	TmuxSession string
	Input       []byte
}

func (CmdSendInput) isCommand() {}

type CmdKillSession struct {
	AgentID     string
	TmuxSession string
}

func (CmdKillSession) isCommand() {}

type CmdRemoveWorktree struct {
	AgentID      string
	RepoPath     string
	WorktreePath string
	Branch       string
}

func (CmdRemoveWorktree) isCommand() {}

type CmdPersistAgentState struct {
	Agent Agent
}

func (CmdPersistAgentState) isCommand() {}

type CmdDeleteAgentRecord struct {
	AgentID string
}

func (CmdDeleteAgentRecord) isCommand() {}

type CmdPersistEvent struct {
	AgentID string
	Event   events.NormalizedEvent
}

func (CmdPersistEvent) isCommand() {}

type CmdNotifyUser struct {
	Title     string
	Body      string
	TargetURL string
}

func (CmdNotifyUser) isCommand() {}
