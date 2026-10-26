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
		a := Agent{
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
			CreatedAt:    inp.Now,
			UpdatedAt:    inp.Now,
		}

		if next.ActiveCount() < next.RunningCap {
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
				Mode:         a.Mode,
				Prompt:       a.Prompt,
				ResumeID:     a.HarnessSessionID,
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
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a

		errEv := events.NewErrorEvent(1, inp.Now, inp.Error, nil)
		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdPersistEvent{AgentID: a.ID, Event: errEv},
			CmdNotifyUser{
				Title:     fmt.Sprintf("Agent failed: %s", a.ID),
				Body:      inp.Error,
				TargetURL: fmt.Sprintf("/agents/%s", a.ID),
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
		if inp.TmuxSession != "" {
			a.TmuxSession = inp.TmuxSession
		}
		if inp.HarnessSessionID != "" {
			a.HarnessSessionID = inp.HarnessSessionID
		}
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a
		cmds = append(cmds, CmdPersistAgentState{Agent: a})
		return next, cmds, nil

	case InputHarnessEvent:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		cmds = append(cmds, CmdPersistEvent{AgentID: a.ID, Event: inp.Event})

		switch inp.Event.Type {
		case events.EventApprovalNeeded:
			if a.State == StateRunning {
				a.State = StateAwaitingApproval
				a.UpdatedAt = inp.Now
				next.Agents[a.ID] = a
				cmds = append(cmds,
					CmdPersistAgentState{Agent: a},
					CmdNotifyUser{
						Title:     fmt.Sprintf("Approval needed: %s", a.ID),
						Body:      "Agent is requesting permission for an action",
						TargetURL: fmt.Sprintf("/agents/%s", a.ID),
					},
				)
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
				if a.State != StateDone && a.State != StateFailed && a.State != StateStopped {
					a.State = StateDone
					a.UpdatedAt = inp.Now
					next.Agents[a.ID] = a
					cmds = append(cmds,
						CmdPersistAgentState{Agent: a},
						CmdNotifyUser{
							Title:     fmt.Sprintf("Agent finished: %s", a.ID),
							Body:      "Agent completed task successfully",
							TargetURL: fmt.Sprintf("/agents/%s", a.ID),
						},
					)
					var queueCmds []Command
					next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
					cmds = append(cmds, queueCmds...)
				}
			}
		}
		return next, cmds, nil

	case InputApprovalRequest:
		a, ok := next.Agents[inp.Request.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.Request.AgentID)
		}

		// Check if this tool is always allowed for this agent
		if allowedTools, exists := next.AlwaysAllowedTools[a.ID]; exists && inp.Request.ToolName != "" && allowedTools[inp.Request.ToolName] {
			cmds = append(cmds,
				CmdResolveApproval{
					AgentID:    a.ID,
					ApprovalID: inp.Request.ID,
					Action:     "allow",
				},
				CmdAuditLog{
					Actor:  "auto-rule",
					Action: "approval",
					Target: a.ID,
					Result: fmt.Sprintf("always_allowed: %s", inp.Request.ToolName),
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

		reqBytes, _ := json.Marshal(inp.Request)
		bodyText := fmt.Sprintf("Tool %s needs approval", inp.Request.ToolName)
		if inp.Request.Command != "" {
			bodyText = fmt.Sprintf("Command: %s", inp.Request.Command)
		} else if inp.Request.FilePath != "" {
			bodyText = fmt.Sprintf("File: %s", inp.Request.FilePath)
		}

		cmds = append(cmds,
			CmdPersistAgentState{Agent: a},
			CmdPersistApproval{
				ID:        inp.Request.ID,
				AgentID:   a.ID,
				Request:   string(reqBytes),
				Decision:  "pending",
				DecidedAt: nil,
			},
			CmdNotifyUser{
				Title:     fmt.Sprintf("Approval needed: %s", a.ID),
				Body:      bodyText,
				TargetURL: fmt.Sprintf("/agents/%s", a.ID),
			},
		)
		return next, cmds, nil

	case InputSendMessage:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State == StateIdle {
			a.State = StateRunning
			a.UpdatedAt = inp.Now
			next.Agents[a.ID] = a
			cmds = append(cmds, CmdPersistAgentState{Agent: a})
		}
		cmds = append(cmds, CmdSendInput{
			AgentID:     a.ID,
			TmuxSession: a.TmuxSession,
			Input:       []byte(inp.Message + "\n"),
		})
		return next, cmds, nil

	case InputApprovalDecision:
		a, ok := next.Agents[inp.AgentID]
		if !ok {
			return state, nil, fmt.Errorf("agent %q not found", inp.AgentID)
		}
		if a.State != StateAwaitingApproval {
			return state, nil, fmt.Errorf("agent %q is not awaiting approval (state: %s)", a.ID, a.State)
		}
		a.State = StateRunning
		a.UpdatedAt = inp.Now
		next.Agents[a.ID] = a
		cmds = append(cmds, CmdPersistAgentState{Agent: a})

		var respBytes []byte
		var action string
		var decisionStr string

		req, hasReq := next.PendingApprovals[inp.ApprovalID]
		if hasReq {
			delete(next.PendingApprovals, inp.ApprovalID)
		}

		if inp.Approved {
			respBytes = []byte("y\n")
			action = "allow"
			if inp.Always && hasReq && req.ToolName != "" {
				decisionStr = "always_allowed"
				if next.AlwaysAllowedTools[a.ID] == nil {
					next.AlwaysAllowedTools[a.ID] = make(map[string]bool)
				}
				next.AlwaysAllowedTools[a.ID][req.ToolName] = true
			} else {
				decisionStr = "approved"
			}
		} else {
			respBytes = []byte("n\n")
			action = "deny"
			decisionStr = "denied"
		}

		cmds = append(cmds,
			CmdResolveApproval{
				AgentID:    a.ID,
				ApprovalID: inp.ApprovalID,
				Action:     action,
			},
			CmdPersistApproval{
				ID:        inp.ApprovalID,
				AgentID:   a.ID,
				Decision:  decisionStr,
				DecidedAt: &inp.Now,
			},
			CmdSendInput{
				AgentID:     a.ID,
				TmuxSession: a.TmuxSession,
				Input:       respBytes,
			},
			CmdAuditLog{
				Actor:  "user",
				Action: "approval",
				Target: a.ID,
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
			// Already terminal, ignore duplicate exit
			return next, cmds, nil
		}

		code := inp.ExitCode
		a.ExitCode = &code
		a.UpdatedAt = inp.Now

		if inp.ExitCode == 0 {
			a.State = StateDone
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdNotifyUser{
					Title:     fmt.Sprintf("Agent finished: %s", a.ID),
					Body:      "Agent exited with code 0",
					TargetURL: fmt.Sprintf("/agents/%s", a.ID),
				},
			)
		} else {
			a.State = StateFailed
			errMsg := fmt.Sprintf("Agent exited with code %d", inp.ExitCode)
			errEv := events.NewErrorEvent(999, inp.Now, errMsg, nil)
			cmds = append(cmds,
				CmdPersistAgentState{Agent: a},
				CmdPersistEvent{AgentID: a.ID, Event: errEv},
				CmdNotifyUser{
					Title:     fmt.Sprintf("Agent failed: %s", a.ID),
					Body:      errMsg,
					TargetURL: fmt.Sprintf("/agents/%s", a.ID),
				},
			)
		}

		next.Agents[a.ID] = a

		var queueCmds []Command
		next, queueCmds = drainQueueIfSlotAvailable(next, inp.Now)
		cmds = append(cmds, queueCmds...)
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
			a.State = StateStopped
			a.UpdatedAt = inp.Now
			next.Agents[a.ID] = a

			// Abort any pending approvals
			for appID, req := range next.PendingApprovals {
				if req.AgentID == a.ID {
					cmds = append(cmds, CmdResolveApproval{
						AgentID:    a.ID,
						ApprovalID: appID,
						Action:     "abort",
					})
					delete(next.PendingApprovals, appID)
				}
			}

			cmds = append(cmds,
				CmdKillSession{AgentID: a.ID, TmuxSession: a.TmuxSession},
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
		wasActive := IsActive(a.State)
		if a.State == StateQueued {
			next.Queue = removeFromSlice(next.Queue, a.ID)
		} else if wasActive {
			// Abort any pending approvals
			for appID, req := range next.PendingApprovals {
				if req.AgentID == a.ID {
					cmds = append(cmds, CmdResolveApproval{
						AgentID:    a.ID,
						ApprovalID: appID,
						Action:     "abort",
					})
					delete(next.PendingApprovals, appID)
				}
			}
			cmds = append(cmds, CmdKillSession{AgentID: a.ID, TmuxSession: a.TmuxSession})
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
		nextID := s.Queue[0]
		s.Queue = s.Queue[1:]

		agent, ok := s.Agents[nextID]
		if !ok {
			continue
		}
		if agent.State != StateQueued {
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
