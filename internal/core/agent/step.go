package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"agent-dashboard/internal/core/events"
)

// Step computes the next ManagerState and side-effect commands from an input.
// Step is a pure function: no IO, no goroutines, no time/randomness.
func Step(state ManagerState, in Input) (ManagerState, []Command, error) {
	next := state.Clone()
	var cmds []Command

	switch inp := in.(type) {
	case InputSubmitAgent:
		if _, exists := next.Agents[inp.Spec.ID]; exists {
			return state, nil, fmt.Errorf("agent with ID %q already exists", inp.Spec.ID)
		}
		if inp.Spec.AutoApprove && inp.Spec.Runtime != "docker" {
			return state, nil, fmt.Errorf("auto-approve is only permitted in the docker runtime")
		}

		a := Agent{
			Image: inp.Spec.Image, SetupCommand: inp.Spec.SetupCommand, EnvVars: inp.Spec.EnvVars, AllowedTools: inp.Spec.AllowedTools,
			ID:           inp.Spec.ID,
			RepoID:       inp.Spec.RepoID,
			RepoPath:     inp.Spec.RepoPath,
			Harness:      inp.Spec.Harness,
			Model:        inp.Spec.Model,
			Runtime:      inp.Spec.Runtime,
			Mode:         inp.Spec.Mode,
			Prompt:       inp.Spec.Prompt,
			Branch:       inp.Spec.Branch,
			BaseBranch:   inp.Spec.BaseBranch,
			WorktreePath: inp.Spec.WorktreePath,
			TmuxSession:  inp.Spec.TmuxSession,
			AutoApprove:  inp.Spec.AutoApprove,
			CreatedAt:    inp.Now,
			UpdatedAt:    inp.Now,
		}

		if len(a.AllowedTools) > 0 {
			next.AlwaysAllowedTools[a.ID] = make(map[string]bool, len(a.AllowedTools))
			for _, tool := range a.AllowedTools {
				next.AlwaysAllowedTools[a.ID][tool] = true
			}
		}
		canRun := next.ActiveCount() < next.RunningCap && next.ActiveCountForRepo(a.RepoID) < next.RepoCap
		if canRun {
			a.State = StateProvisioning
			next.Agents[a.ID] = a
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdCreateWorktree{
					AgentID:      a.ID,
					RepoPath:     a.RepoPath,
					BaseBranch:   a.BaseBranch,
					Branch:       a.Branch,
					WorktreePath: a.WorktreePath,
				},
			)
		} else {
			a.State = StateQueued
			next.Agents[a.ID] = a
			next.Queue = append(next.Queue, a.ID)
			cmds = append(cmds, CmdPersistAgentState{Agent: a})
		}
		return next, cmds, nil

	case InputWorktreeReady:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State != StateProvisioning {
			return state, nil, nil
		}
		if err := ValidateStateTransition(a.State, StateRunning); err != nil {
			return state, nil, err
		}
		a.State = StateRunning
		a.WorktreePath = inp.WorktreePath
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a

		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdStartSession{
				AgentID:      a.ID,
				WorktreePath: a.WorktreePath,
				TmuxSession:  a.TmuxSession,
				Harness:      a.Harness,
				Model:        a.Model,
				Runtime:      a.Runtime,
				Mode:         a.Mode,
				Prompt:       a.Prompt,
				ResumeID:     a.HarnessSessionID,
				AutoApprove:  a.AutoApprove,
			},
		)
		return next, cmds, nil

	case InputWorktreeFailed:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if err := ValidateStateTransition(a.State, StateFailed); err != nil {
			return state, nil, err
		}
		a.State = StateFailed
		a.FailureReason = "worktree_failed"
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a

		errEv := events.NewErrorEvent(1, inp.Now, inp.Error, nil)
		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdPersistEvent{AgentID: a.ID, Event: errEv},
			CmdNotifyUser{
				Urgent: true, Title: fmt.Sprintf("Agent failed: %s", a.ID),
				Body:      inp.Error,
				TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
			},
		)

		var queueCmds []Command
		next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
		cmds = append(cmds, queueCmds...)
		return next, cmds, nil

	case InputSessionStarted:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if (a.State != StateRunning && a.State != StateAwaitingApproval && a.State != StateIdle) || a.TmuxSession != inp.TmuxSession {
			return state, nil, nil
		}
		a.TmuxSession = inp.TmuxSession
		a.HarnessSessionID = inp.HarnessSessionID
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a
		cmds = append(cmds, CmdPersistAgentState{Agent: a})
		return next, cmds, nil

	case InputHarnessEvent:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if !IsActive(a.State) {
			return state, nil, nil
		}
		cmds = append(cmds, CmdPersistEvent{AgentID: a.ID, Event: inp.Event})

		switch inp.Event.Type {
		case events.EventApprovalNeeded:
			id, _ := inp.Event.Payload["approval_id"].(string)
			if id == "" {
				return next, cmds, nil
			}
			command, _ := inp.Event.Payload["command"].(string)
			tool, _ := inp.Event.Payload["target"].(string)
			return Step(next, InputApprovalRequest{Now: inp.Now, Request: ApprovalRequest{ID: id, AgentID: a.ID, ToolName: tool, Command: command, CreatedAt: inp.Now}})
		case events.EventMessage, events.EventToolCall, events.EventToolResult:
			if a.State == StateAwaitingApproval && pendingForAgent(next, a.ID) == 0 {
				a.State = StateRunning
				a.UpdatedAt = inp.Now
				next.Agents[a.ID] = a
				cmds = append(cmds, CmdPersistAgentState{Agent: a})
			}
		case events.EventDone:
			if a.Mode == "interactive" {
				if a.State == StateRunning {
					a.State = StateIdle
					a.UpdatedAt = inp.Now
					next.Agents[a.ID] = a
					cmds = append(cmds, CmdPersistAgentState{Agent: a})
				}
			} else {
				if err := ValidateStateTransition(a.State, StateDone); err == nil {
					a.State = StateDone
					a.UpdatedAt = inp.Now
					next.Agents[a.ID] = a
					cmds = append(cmds,
						CmdPersistAgentState{Agent: a},
						CmdNotifyUser{
							Title:     fmt.Sprintf("Agent done: %s", a.ID),
							Body:      "Agent finished its task successfully",
							TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
						},
					)
					next.Stopping[a.ID] = a
					var queueCmds []Command
					next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
					cmds = append(cmds, queueCmds...)
				}
			}
		case events.EventError:
			if err := ValidateStateTransition(a.State, StateFailed); err == nil {
				a.State = StateFailed
				a.FailureReason = "harness_error"
				a.UpdatedAt = inp.Now
				next.Agents[a.ID] = a
				cmds = append(cmds,
					CmdPersistAgentState{Agent: a},
					CmdNotifyUser{
						Urgent: true, Title: fmt.Sprintf("Agent error: %s", a.ID),
						Body:      "Harness reported a critical error",
						TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
					},
				)
				next.Stopping[a.ID] = a
				var queueCmds []Command
				next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
				cmds = append(cmds, queueCmds...)
			}
		}
		if IsTerminal(next.Agents[a.ID].State) {
			for id, req := range next.PendingApprovals {
				if req.AgentID == a.ID {
					delete(next.PendingApprovals, id)
					now := inp.Now
					cmds = append(cmds, CmdPersistApproval{ID: id, AgentID: a.ID, Decision: "aborted", DecidedAt: &now}, CmdResolveApproval{AgentID: a.ID, ApprovalID: id, Action: "abort"})
				}
			}
			next.Stopping[a.ID] = a
			cmds = append([]Command{CmdKillSession{AgentID: a.ID, TmuxSession: a.TmuxSession, Runtime: a.Runtime}}, cmds...)
		}
		return next, cmds, nil

	case InputSendMessage:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State != StateRunning && a.State != StateIdle {
			return state, nil, fmt.Errorf("cannot send message to agent in state %q", a.State)
		}
		a.State = StateRunning
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a

		userEv := events.NewMessageEvent(0, inp.Now, "user", inp.Message)
		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdPersistEvent{AgentID: a.ID, Event: userEv},
			CmdSendInput{
				AgentID:     a.ID,
				TmuxSession: a.TmuxSession,
				Input:       []byte(inp.Message + "\n"),
			},
		)
		return next, cmds, nil

	case InputApprovalRequest:
		a, ok := next.Agents[inp.Request.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.Request.AgentID)
		}

		if a.State != StateRunning && a.State != StateAwaitingApproval {
			return state, nil, fmt.Errorf("agent cannot request approval in state %q", a.State)
		}
		if req, ok := next.PendingApprovals[inp.Request.ID]; ok {
			if req.AgentID != a.ID {
				return state, nil, fmt.Errorf("approval belongs to another agent")
			}
			return state, nil, nil
		}
		// Check if auto-approve is active for this agent
		if a.AutoApprove && a.Runtime == "docker" {
			reqJSON, _ := json.Marshal(inp.Request)
			now := inp.Now
			cmds = append(cmds,
				CmdPersistApproval{
					ID:        inp.Request.ID,
					AgentID:   a.ID,
					Request:   string(reqJSON),
					Decision:  "approved",
					DecidedAt: &now,
				},
				CmdResolveApproval{
					AgentID:    a.ID,
					ApprovalID: inp.Request.ID,
					Action:     "allow",
				},
			)
			return next, cmds, nil
		}

		// Check if tool is always allowed for this agent
		if allowed, ok := next.AlwaysAllowedTools[a.ID]; ok && allowed[inp.Request.ToolName] {
			reqJSON, _ := json.Marshal(inp.Request)
			now := inp.Now
			cmds = append(cmds,
				CmdPersistApproval{
					ID:        inp.Request.ID,
					AgentID:   a.ID,
					Request:   string(reqJSON),
					Decision:  "always_allowed",
					DecidedAt: &now,
				},
				CmdResolveApproval{
					AgentID:    a.ID,
					ApprovalID: inp.Request.ID,
					Action:     "allow",
				},
			)
			return next, cmds, nil
		}

		if err := ValidateStateTransition(a.State, StateAwaitingApproval); err != nil {
			return state, nil, err
		}
		a.State = StateAwaitingApproval
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a
		next.PendingApprovals[inp.Request.ID] = inp.Request

		reqJSON, _ := json.Marshal(inp.Request)
		appEv := events.NewApprovalNeededEvent(0, inp.Now, inp.Request.ID, inp.Request.Command, inp.Request.ToolName, inp.Request.ToolInput)

		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdPersistApproval{
				ID:       inp.Request.ID,
				AgentID:  a.ID,
				Request:  string(reqJSON),
				Decision: "pending",
			},
			CmdPersistEvent{AgentID: a.ID, Event: appEv},
			CmdNotifyUser{
				Urgent: true, Title: fmt.Sprintf("Approval needed: %s", a.ID),
				Body:      fmt.Sprintf("Tool %s requires confirmation", inp.Request.ToolName),
				TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
			},
		)
		return next, cmds, nil

	case InputApprovalDecision:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		_, hasPending := next.PendingApprovals[inp.ApprovalID]
		if !hasPending || next.PendingApprovals[inp.ApprovalID].AgentID != a.ID || a.State != StateAwaitingApproval {
			return state, nil, fmt.Errorf("no pending approval %q for agent %q", inp.ApprovalID, inp.AgentID)
		}

		req := next.PendingApprovals[inp.ApprovalID]
		delete(next.PendingApprovals, inp.ApprovalID)

		decisionStr := "denied"
		actionStr := "deny"
		if inp.Approved {
			decisionStr = "approved"
			actionStr = "allow"
			if inp.Always {
				decisionStr = "always_allowed"
				if _, exists := next.AlwaysAllowedTools[a.ID]; !exists {
					next.AlwaysAllowedTools[a.ID] = make(map[string]bool)
				}
				next.AlwaysAllowedTools[a.ID][req.ToolName] = true
			}
		}

		if pendingForAgent(next, a.ID) == 0 && a.State == StateAwaitingApproval {
			a.State = StateRunning
			a.UpdatedAt = inp.Now
			next.Agents[a.ID] = a
			cmds = append(cmds, CmdPersistAgentState{Agent: a})
		}

		decidedAt := inp.Now
		reqJSON, _ := json.Marshal(req)
		cmds = append(cmds,
			CmdPersistApproval{
				ID:        inp.ApprovalID,
				AgentID:   a.ID,
				Request:   string(reqJSON),
				Decision:  decisionStr,
				DecidedAt: &decidedAt,
			},
			CmdResolveApproval{
				AgentID:    a.ID,
				ApprovalID: inp.ApprovalID,
				Action:     actionStr,
			},
			CmdAuditLog{
				Actor:  "user",
				Action: "approval_decision",
				Target: fmt.Sprintf("agent:%s:tool:%s", a.ID, req.ToolName),
				Result: decisionStr,
			},
		)
		return next, cmds, nil

	case InputProcessExited:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if IsTerminal(a.State) {
			return Step(next, InputSessionStopped{Now: inp.Now, AgentID: a.ID})
		}
		if !IsActive(a.State) {
			return state, nil, fmt.Errorf("agent %q is in non-active state %q, cannot process exit", a.ID, a.State)
		}

		code := inp.ExitCode
		a.ExitCode = &code
		a.UpdatedAt = inp.Now

		if inp.OOMKilled || inp.ExitCode == 137 {
			a.State = StateFailed
			a.FailureReason = "oom_killed"
			errMsg := "Agent terminated: out of memory (OOM killed, 700 MB limit exceeded)"
			errEv := events.NewErrorEvent(999, inp.Now, errMsg, nil)
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdPersistEvent{AgentID: a.ID, Event: errEv},
				CmdNotifyUser{
					Urgent: true, Title: fmt.Sprintf("Agent failed (OOM): %s", a.ID),
					Body:      errMsg,
					TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
				},
			)
		} else if inp.ExitCode == 0 {
			a.State = StateDone
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdNotifyUser{
					Title:     fmt.Sprintf("Agent finished: %s", a.ID),
					Body:      "Agent exited with code 0",
					TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
				},
			)
		} else {
			a.State = StateFailed
			a.FailureReason = "exit_error"
			errMsg := fmt.Sprintf("Agent exited with code %d", inp.ExitCode)
			errEv := events.NewErrorEvent(999, inp.Now, errMsg, nil)
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdPersistEvent{AgentID: a.ID, Event: errEv},
				CmdNotifyUser{
					Urgent: true, Title: fmt.Sprintf("Agent failed: %s", a.ID),
					Body:      errMsg,
					TargetURL: fmt.Sprintf("/?agent_id=%s", a.ID),
				},
			)
		}

		next.Agents[a.ID] = a
		delete(next.Stopping, a.ID)
		for id, req := range next.PendingApprovals {
			if req.AgentID == a.ID {
				delete(next.PendingApprovals, id)
				now := inp.Now
				cmds = append(cmds, CmdPersistApproval{ID: id, AgentID: a.ID, Decision: "aborted", DecidedAt: &now}, CmdResolveApproval{AgentID: a.ID, ApprovalID: id, Action: "abort"})
			}
		}

		var queueCmds []Command
		next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
		cmds = append(cmds, queueCmds...)
		return next, cmds, nil

	case InputCancelQueued:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State != StateQueued {
			return state, nil, fmt.Errorf("agent %q is not queued (current: %s)", inp.AgentID, a.State)
		}
		next.Queue = removeFromSlice(next.Queue, a.ID)
		a.State = StateStopped
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a
		cmds = append(cmds, CmdPersistAgentState{Agent: a})
		return next, cmds, nil

	case InputStopRequested:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State == StateQueued {
			// Remove from queue
			next.Queue = removeFromSlice(next.Queue, a.ID)
			a.State = StateStopped
			a.UpdatedAt = inp.Now
			next.Agents[a.ID] = a
			cmds = append(cmds, CmdPersistAgentState{Agent: a})
			return next, cmds, nil
		}

		if IsActive(a.State) {
			next.Stopping[a.ID] = a
			a.State = StateStopped
			a.UpdatedAt = inp.Now
			next.Agents[a.ID] = a

			// Abort any pending approvals
			for appID, req := range next.PendingApprovals {
				if req.AgentID == a.ID {
					now := inp.Now
					cmds = append(cmds, CmdPersistApproval{ID: appID, AgentID: a.ID, Decision: "aborted", DecidedAt: &now})
					cmds = append(cmds, CmdResolveApproval{
						AgentID:    a.ID,
						ApprovalID: appID,
						Action:     "abort",
					})
					delete(next.PendingApprovals, appID)
				}
			}

			cmds = append(cmds,
				CmdKillSession{AgentID: a.ID, TmuxSession: a.TmuxSession, Runtime: a.Runtime},
				CmdPersistAgentState{Agent: a},
			)
			next, queueCmds := drainQueueIfSlotAvailable(next, inp.Now)
			cmds = append(cmds, queueCmds...)
			return next, cmds, nil
		}
		return next, cmds, nil

	case InputDeleteRequested:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if _, stopping := next.Stopping[a.ID]; stopping {
			next.Deleting[a.ID] = true
			return next, nil, nil
		}
		if IsActive(a.State) {
			next.Deleting[a.ID] = true
			return Step(next, InputStopRequested{Now: inp.Now, AgentID: a.ID})
		}
		wasActive := IsActive(a.State)
		next.Queue = removeFromSlice(next.Queue, a.ID)
		if wasActive {
			// Abort any pending approvals
			for appID, req := range next.PendingApprovals {
				if req.AgentID == a.ID {
					now := inp.Now
					cmds = append(cmds, CmdPersistApproval{ID: appID, AgentID: a.ID, Decision: "aborted", DecidedAt: &now})
					cmds = append(cmds, CmdResolveApproval{
						AgentID:    a.ID,
						ApprovalID: appID,
						Action:     "abort",
					})
					delete(next.PendingApprovals, appID)
				}
			}
			cmds = append(cmds, CmdKillSession{AgentID: a.ID, TmuxSession: a.TmuxSession, Runtime: a.Runtime})
		}

		if a.WorktreePath != "" {
			cmds = append(cmds, CmdRemoveWorktree{
				AgentID:      a.ID,
				RepoPath:     a.RepoPath,
				WorktreePath: a.WorktreePath,
				Branch:       a.Branch,
			})
		}
		cmds = append(cmds, CmdDeleteAgentRecord{AgentID: a.ID})
		delete(next.Agents, a.ID)
		delete(next.AlwaysAllowedTools, a.ID)

		if wasActive {
			var queueCmds []Command
			next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
			cmds = append(cmds, queueCmds...)
		}
		return next, cmds, nil

	case InputSessionStopped:
		if _, ok := next.Stopping[inp.AgentID]; !ok {
			return state, nil, nil
		}
		delete(next.Stopping, inp.AgentID)
		if next.Deleting[inp.AgentID] {
			delete(next.Deleting, inp.AgentID)
			st, cs, err := Step(next, InputDeleteRequested(inp))
			if err != nil {
				return state, nil, err
			}
			st, drain := drainQueueIfSlotAvailable(st, inp.Now)
			return st, append(cs, drain...), nil
		}
		next, cmds = drainQueueIfSlotAvailable(next, inp.Now)
		return next, cmds, nil
	case InputResumeRequested:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent not found")
		}
		if !IsTerminal(a.State) || next.Deleting[a.ID] {
			return state, nil, fmt.Errorf("only a finished agent can resume")
		}
		if _, stopping := next.Stopping[a.ID]; stopping {
			return state, nil, fmt.Errorf("agent is still stopping")
		}
		if a.HarnessSessionID == "" {
			return state, nil, fmt.Errorf("harness did not report a resumable session")
		}
		a.TmuxSession = inp.TmuxSession
		a.Resuming = true
		a.OutputCursor = 0
		a.ExitCode = nil
		a.FailureReason = ""
		a.UpdatedAt = inp.Now
		a.State = StateQueued
		next.Agents[a.ID] = a
		next.Queue = append(next.Queue, a.ID)
		next, cmds = drainQueueIfSlotAvailable(next, inp.Now)
		return next, cmds, nil
	case InputInterruptRequested:
		a, ok := next.Agents[inp.AgentID]
		if !ok || !IsActive(a.State) {
			return state, nil, fmt.Errorf("agent is not active")
		}
		return next, []Command{CmdInterruptSession{AgentID: a.ID, TmuxSession: a.TmuxSession, Runtime: a.Runtime}}, nil

	case InputReconcileBoot:
		for id, a := range next.Agents {
			if IsActive(a.State) {
				if a.TmuxSession != "" && !inp.RunningTmuxSessions[a.TmuxSession] {
					// tmux session disappeared while dashboard was offline
					a.State = StateFailed
					a.UpdatedAt = inp.Now
					next.Agents[id] = a
					cmds = append(cmds, CmdPersistAgentState{Agent: a})
				}
			}
		}
		var queueCmds []Command
		next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
		cmds = append(cmds, queueCmds...)
		return next, cmds, nil

	default:
		return state, nil, fmt.Errorf("unknown input type %T", in)
	}
}

func drainQueueIfSlotAvailable(s ManagerState, now time.Time) (ManagerState, []Command) {
	var cmds []Command
	for s.ActiveCount() < s.RunningCap && len(s.Queue) > 0 {
		foundIdx := -1
		for i, qID := range s.Queue {
			ag, ok := s.Agents[qID]
			if !ok || ag.State != StateQueued {
				continue
			}
			if s.ActiveCountForRepo(ag.RepoID) < s.RepoCap {
				foundIdx = i
				break
			}
		}
		if foundIdx == -1 {
			// No queued agent can run without violating per-repo cap
			break
		}

		nextID := s.Queue[foundIdx]
		s.Queue = removeFromSlice(s.Queue, nextID)

		agent, ok := s.Agents[nextID]
		if !ok || agent.State != StateQueued {
			continue
		}
		agent.State = StateProvisioning
		agent.UpdatedAt = now
		s.Agents[agent.ID] = agent

		cmds = append(cmds,
			CmdPersistAgentState{Agent: agent},
			CmdCreateWorktree{
				AgentID:      agent.ID,
				RepoPath:     agent.RepoPath,
				BaseBranch:   agent.BaseBranch,
				Branch:       agent.Branch,
				WorktreePath: agent.WorktreePath,
			},
		)
	}
	return s, cmds
}

func removeFromSlice(slice []string, val string) []string {
	res := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != val {
			res = append(res, item)
		}
	}
	return res
}

func pendingForAgent(s ManagerState, id string) int {
	n := 0
	for _, r := range s.PendingApprovals {
		if r.AgentID == id {
			n++
		}
	}
	return n
}
