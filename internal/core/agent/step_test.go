package agent

import (
	"testing"
	"time"

	"agent-dashboard/internal/core/events"
)

func TestAgentLifecycleTransitions(t *testing.T) {
	now := time.Now().UTC()
	manager := NewManagerState(1)

	spec := AgentSpec{
		ID:           "agent-1",
		RepoID:       "repo-1",
		RepoPath:     "/workspace/repo-1",
		Harness:      "claude",
		Model:        "claude-sonnet-5-5",
		Runtime:      "host",
		Mode:         "task",
		Prompt:       "Fix bug",
		Branch:       "agent/fix-bug",
		BaseBranch:   "main",
		WorktreePath: "/workspace/repo-1-wt-1",
		TmuxSession:  "tmux-agent-1",
	}

	// 1. Submit agent with capacity 1 -> StateProvisioning
	var cmds []Command
	var err error
	manager, cmds, err = Step(manager, InputSubmitAgent{Now: now, Spec: spec})
	if err != nil {
		t.Fatalf("submit agent failed: %v", err)
	}
	if a := manager.Agents["agent-1"]; a.State != StateProvisioning {
		t.Fatalf("expected StateProvisioning, got %s", a.State)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}

	// 2. Worktree ready -> StateRunning
	manager, cmds, err = Step(manager, InputWorktreeReady{Now: now, AgentID: "agent-1", WorktreePath: spec.WorktreePath})
	if err != nil {
		t.Fatalf("worktree ready failed: %v", err)
	}
	if a := manager.Agents["agent-1"]; a.State != StateRunning {
		t.Fatalf("expected StateRunning, got %s", a.State)
	}
	hasStartCmd := false
	for _, c := range cmds {
		if _, ok := c.(CmdStartSession); ok {
			hasStartCmd = true
		}
	}
	if !hasStartCmd {
		t.Fatalf("expected CmdStartSession in commands")
	}

	// 3. Approval needed event -> StateAwaitingApproval
	appEv := events.NewApprovalNeededEvent(1, now, "app-1", "npm install", "package.json", nil)
	manager, _, err = Step(manager, InputHarnessEvent{Now: now, AgentID: "agent-1", Event: appEv})
	if err != nil {
		t.Fatalf("harness event failed: %v", err)
	}
	if a := manager.Agents["agent-1"]; a.State != StateAwaitingApproval {
		t.Fatalf("expected StateAwaitingApproval, got %s", a.State)
	}

	// 4. Approval decision -> StateRunning
	manager, _, err = Step(manager, InputApprovalDecision{Now: now, AgentID: "agent-1", ApprovalID: "app-1", Approved: true})
	if err != nil {
		t.Fatalf("approval decision failed: %v", err)
	}
	if a := manager.Agents["agent-1"]; a.State != StateRunning {
		t.Fatalf("expected StateRunning, got %s", a.State)
	}

	// 5. Done event -> StateDone
	doneEv := events.NewDoneEvent(2, now, "Complete", "completed")
	manager, _, err = Step(manager, InputHarnessEvent{Now: now, AgentID: "agent-1", Event: doneEv})
	if err != nil {
		t.Fatalf("done event failed: %v", err)
	}
	if a := manager.Agents["agent-1"]; a.State != StateDone {
		t.Fatalf("expected StateDone, got %s", a.State)
	}
	manager, _, _ = Step(manager, InputSessionStopped{Now: now, AgentID: "agent-1"})
	if manager.ActiveCount() != 0 {
		t.Fatalf("expected 0 active agents, got %d", manager.ActiveCount())
	}
}

func TestInteractiveAgentLifecycle(t *testing.T) {
	now := time.Now().UTC()
	manager := NewManagerState(1)

	spec := AgentSpec{
		ID:           "agent-i",
		RepoID:       "repo-1",
		RepoPath:     "/workspace/repo-1",
		Harness:      "claude",
		Runtime:      "host",
		Mode:         "interactive",
		Branch:       "agent/chat",
		WorktreePath: "/workspace/repo-1-wt-i",
		TmuxSession:  "tmux-agent-i",
	}

	manager, _, _ = Step(manager, InputSubmitAgent{Now: now, Spec: spec})
	manager, _, _ = Step(manager, InputWorktreeReady{Now: now, AgentID: spec.ID, WorktreePath: spec.WorktreePath})

	// When an interactive agent finishes turn, EventDone transitions it to StateIdle
	doneEv := events.NewDoneEvent(1, now, "Finished turn", "completed")
	manager, _, _ = Step(manager, InputHarnessEvent{Now: now, AgentID: spec.ID, Event: doneEv})
	if a := manager.Agents[spec.ID]; a.State != StateIdle {
		t.Fatalf("expected StateIdle in interactive mode, got %s", a.State)
	}

	// Sending user message resumes running
	var cmds []Command
	manager, cmds, _ = Step(manager, InputSendMessage{Now: now, AgentID: spec.ID, Message: "tell me more"})
	if a := manager.Agents[spec.ID]; a.State != StateRunning {
		t.Fatalf("expected StateRunning after send message, got %s", a.State)
	}
	hasSendCmd := false
	for _, c := range cmds {
		if sc, ok := c.(CmdSendInput); ok && string(sc.Input) == "tell me more\n" {
			hasSendCmd = true
		}
	}
	if !hasSendCmd {
		t.Fatalf("expected CmdSendInput with user message")
	}
}

func TestQueueConcurrencyInvariant(t *testing.T) {
	now := time.Now().UTC()
	// Default cap 1, max 2
	manager := NewManagerState(1)

	// Submit Agent 1 -> should be Provisioning
	spec1 := AgentSpec{ID: "agent-1", RepoID: "r1", WorktreePath: "/wt-1", TmuxSession: "t-1"}
	manager, _, err := Step(manager, InputSubmitAgent{Now: now, Spec: spec1})
	if err != nil {
		t.Fatalf("submit 1 failed: %v", err)
	}
	if manager.Agents["agent-1"].State != StateProvisioning {
		t.Fatalf("agent 1 should be provisioning")
	}

	// Submit Agent 2 -> active count is 1 == cap 1, so should be StateQueued
	spec2 := AgentSpec{ID: "agent-2", RepoID: "r1", WorktreePath: "/wt-2", TmuxSession: "t-2"}
	manager, _, err = Step(manager, InputSubmitAgent{Now: now, Spec: spec2})
	if err != nil {
		t.Fatalf("submit 2 failed: %v", err)
	}
	if manager.Agents["agent-2"].State != StateQueued {
		t.Fatalf("agent 2 should be queued")
	}
	if len(manager.Queue) != 1 || manager.Queue[0] != "agent-2" {
		t.Fatalf("queue should contain agent-2")
	}

	// Submit Agent 3 -> should also be StateQueued
	spec3 := AgentSpec{ID: "agent-3", RepoID: "r1", WorktreePath: "/wt-3", TmuxSession: "t-3"}
	manager, _, err = Step(manager, InputSubmitAgent{Now: now, Spec: spec3})
	if err != nil {
		t.Fatalf("submit 3 failed: %v", err)
	}
	if manager.Agents["agent-3"].State != StateQueued {
		t.Fatalf("agent 3 should be queued")
	}
	if len(manager.Queue) != 2 || manager.Queue[1] != "agent-3" {
		t.Fatalf("queue should contain agent-2 then agent-3 in FIFO order")
	}

	// Invariant: active count must be <= runningCap
	if manager.ActiveCount() > manager.RunningCap {
		t.Fatalf("active count %d exceeded cap %d", manager.ActiveCount(), manager.RunningCap)
	}

	// Agent 1 finishes -> agent-2 should automatically transition to StateProvisioning
	manager, cmds, err := Step(manager, InputProcessExited{Now: now, AgentID: "agent-1", ExitCode: 0})
	if err != nil {
		t.Fatalf("exit agent-1 failed: %v", err)
	}
	if manager.Agents["agent-1"].State != StateDone {
		t.Fatalf("agent-1 should be done")
	}
	if manager.Agents["agent-2"].State != StateProvisioning {
		t.Fatalf("agent-2 should have been dequeued to provisioning, got %s", manager.Agents["agent-2"].State)
	}
	if len(manager.Queue) != 1 || manager.Queue[0] != "agent-3" {
		t.Fatalf("queue should now have agent-3 at head")
	}

	// Check that CmdCreateWorktree was emitted for agent-2
	foundWt2 := false
	for _, c := range cmds {
		if wt, ok := c.(CmdCreateWorktree); ok && wt.AgentID == "agent-2" {
			foundWt2 = true
		}
	}
	if !foundWt2 {
		t.Fatalf("expected CmdCreateWorktree for agent-2")
	}

	// Agent 2 fails -> agent-3 should be dequeued
	manager, _, _ = Step(manager, InputWorktreeFailed{Now: now, AgentID: "agent-2", Error: "disk error"})
	if manager.Agents["agent-2"].State != StateFailed {
		t.Fatalf("agent-2 should be failed")
	}
	if manager.Agents["agent-3"].State != StateProvisioning {
		t.Fatalf("agent-3 should now be provisioning")
	}
	if len(manager.Queue) != 0 {
		t.Fatalf("queue should be empty")
	}
}

func TestReconcileBoot(t *testing.T) {
	now := time.Now().UTC()
	manager := NewManagerState(1)

	spec1 := AgentSpec{ID: "agent-1", TmuxSession: "session-alive"}

	manager, _, _ = Step(manager, InputSubmitAgent{Now: now, Spec: spec1})
	manager, _, _ = Step(manager, InputWorktreeReady{Now: now, AgentID: "agent-1", WorktreePath: "/wt-1"})

	// Force agent 2 into state running for test
	a2 := manager.Agents["agent-1"]
	a2.ID = "agent-2"
	a2.TmuxSession = "session-dead"
	manager.Agents["agent-2"] = a2

	runningTmux := map[string]bool{
		"session-alive": true,
	}

	manager, cmds, err := Step(manager, InputReconcileBoot{Now: now, RunningTmuxSessions: runningTmux})
	if err != nil {
		t.Fatalf("reconcile boot failed: %v", err)
	}

	if manager.Agents["agent-1"].State != StateRunning {
		t.Fatalf("agent-1 should remain running")
	}
	if manager.Agents["agent-2"].State != StateFailed {
		t.Fatalf("agent-2 should transition to failed, got %s", manager.Agents["agent-2"].State)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 persist command for agent-2, got %d", len(cmds))
	}
}

func TestIllegalTransitionsFail(t *testing.T) {
	now := time.Now().UTC()
	manager := NewManagerState(1)

	spec := AgentSpec{ID: "agent-1", TmuxSession: "s-1"}
	manager, _, _ = Step(manager, InputSubmitAgent{Now: now, Spec: spec})
	// Agent is in StateProvisioning. Trying to give ApprovalDecision directly should fail.
	_, _, err := Step(manager, InputApprovalDecision{Now: now, AgentID: "agent-1", ApprovalID: "app-1", Approved: true})
	if err == nil {
		t.Fatalf("expected error for illegal approval decision in provisioning state")
	}
}

func TestStructuredApprovalLifecycle(t *testing.T) {
	now := time.Now().UTC()
	manager := NewManagerState(1)

	spec := AgentSpec{
		ID:           "agent-appr",
		RepoID:       "repo-1",
		RepoPath:     "/workspace/repo-1",
		Harness:      "claude",
		WorktreePath: "/workspace/repo-1-wt",
		TmuxSession:  "tmux-appr",
	}

	manager, _, _ = Step(manager, InputSubmitAgent{Now: now, Spec: spec})
	manager, _, _ = Step(manager, InputWorktreeReady{Now: now, AgentID: spec.ID, WorktreePath: spec.WorktreePath})

	// 1. Hook requests approval for Bash tool
	req := ApprovalRequest{
		ID:       "app-1",
		AgentID:  spec.ID,
		ToolName: "Bash",
		Command:  "npm test",
	}

	var cmds []Command
	var err error
	manager, cmds, err = Step(manager, InputApprovalRequest{Now: now, Request: req})
	if err != nil {
		t.Fatalf("InputApprovalRequest failed: %v", err)
	}
	if a := manager.Agents[spec.ID]; a.State != StateAwaitingApproval {
		t.Fatalf("expected StateAwaitingApproval, got %s", a.State)
	}

	// Verify CmdPersistApproval and CmdNotifyUser
	var hasPersistAppr, hasNotify bool
	for _, c := range cmds {
		if pa, ok := c.(CmdPersistApproval); ok && pa.ID == "app-1" {
			hasPersistAppr = true
		}
		if _, ok := c.(CmdNotifyUser); ok {
			hasNotify = true
		}
	}
	if !hasPersistAppr || !hasNotify {
		t.Fatalf("expected CmdPersistApproval and CmdNotifyUser, got %v", cmds)
	}

	// 2. User approves with Always: true
	manager, cmds, err = Step(manager, InputApprovalDecision{
		Now:        now,
		AgentID:    spec.ID,
		ApprovalID: "app-1",
		Approved:   true,
		Always:     true,
	})
	if err != nil {
		t.Fatalf("InputApprovalDecision failed: %v", err)
	}
	if a := manager.Agents[spec.ID]; a.State != StateRunning {
		t.Fatalf("expected StateRunning after approval, got %s", a.State)
	}

	var hasResolveAllow bool
	for _, c := range cmds {
		if ra, ok := c.(CmdResolveApproval); ok && ra.Action == "allow" {
			hasResolveAllow = true
		}
	}
	if !hasResolveAllow {
		t.Fatalf("expected CmdResolveApproval with action allow, got %v", cmds)
	}

	// 3. Next approval request for same tool (Bash) should auto-allow!
	req2 := ApprovalRequest{
		ID:       "app-2",
		AgentID:  spec.ID,
		ToolName: "Bash",
		Command:  "npm run build",
	}
	manager, cmds, err = Step(manager, InputApprovalRequest{Now: now, Request: req2})
	if err != nil {
		t.Fatalf("InputApprovalRequest 2 failed: %v", err)
	}
	// Agent should remain running (not awaiting approval)
	if a := manager.Agents[spec.ID]; a.State != StateRunning {
		t.Fatalf("expected StateRunning (auto-allowed), got %s", a.State)
	}
	var hasAutoAllow bool
	for _, c := range cmds {
		if ra, ok := c.(CmdResolveApproval); ok && ra.ApprovalID == "app-2" && ra.Action == "allow" {
			hasAutoAllow = true
		}
	}
	if !hasAutoAllow {
		t.Fatalf("expected auto CmdResolveApproval allow for app-2, got %v", cmds)
	}
}
