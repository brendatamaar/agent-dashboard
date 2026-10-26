package agent

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"agent-dashboard/internal/core/events"
)

func TestPropertyConcurrencyInvariant(t *testing.T) {
	// Seed deterministic pseudo-random generator
	r := rand.New(rand.NewSource(42))
	now := time.Now().UTC()

	for trial := 0; trial < 100; trial++ {
		capLimit := 1 + r.Intn(2) // 1 or 2
		mgr := NewManagerState(capLimit)

		agentIDs := []string{"a-1", "a-2", "a-3", "a-4", "a-5"}

		// Generate random sequence of 50 actions
		for stepIdx := 0; stepIdx < 50; stepIdx++ {
			agentID := agentIDs[r.Intn(len(agentIDs))]
			actionType := r.Intn(9)

			var in Input
			switch actionType {
			case 0:
				in = InputSubmitAgent{
					Now: now,
					Spec: AgentSpec{
						ID:           agentID,
						RepoID:       "repo-1",
						RepoPath:     "/workspace/repo-1",
						Harness:      "claude",
						WorktreePath: fmt.Sprintf("/wt-%s", agentID),
						TmuxSession:  fmt.Sprintf("tmux-%s", agentID),
					},
				}
			case 1:
				in = InputWorktreeReady{
					Now:          now,
					AgentID:      agentID,
					WorktreePath: fmt.Sprintf("/wt-%s", agentID),
				}
			case 2:
				in = InputWorktreeFailed{
					Now:     now,
					AgentID: agentID,
					Error:   "simulated error",
				}
			case 3:
				in = InputHarnessEvent{
					Now:     now,
					AgentID: agentID,
					Event:   events.NewApprovalNeededEvent(int64(stepIdx), now, "app", "cmd", "tgt", nil),
				}
			case 4:
				in = InputApprovalDecision{
					Now:        now,
					AgentID:    agentID,
					ApprovalID: "app",
					Approved:   r.Intn(2) == 1,
				}
			case 5:
				in = InputHarnessEvent{
					Now:     now,
					AgentID: agentID,
					Event:   events.NewDoneEvent(int64(stepIdx), now, "done", "completed"),
				}
			case 6:
				in = InputProcessExited{
					Now:      now,
					AgentID:  agentID,
					ExitCode: r.Intn(2),
				}
			case 7:
				in = InputStopRequested{
					Now:     now,
					AgentID: agentID,
				}
			case 8:
				in = InputDeleteRequested{
					Now:     now,
					AgentID: agentID,
				}
			}

			next, _, _ := Step(mgr, in)
			mgr = next

			// INVARIANT 1: ActiveCount must never exceed capLimit
			if mgr.ActiveCount() > mgr.RunningCap {
				t.Fatalf("Invariant violated at trial %d step %d: ActiveCount %d > RunningCap %d",
					trial, stepIdx, mgr.ActiveCount(), mgr.RunningCap)
			}

			// INVARIANT 2: No queued agent should be active
			for _, qID := range mgr.Queue {
				if a, ok := mgr.Agents[qID]; ok && IsActive(a.State) {
					t.Fatalf("Invariant violated: agent %s in queue has active state %s", qID, a.State)
				}
			}

			// INVARIANT 3: Terminal agents must not be active
			for id, a := range mgr.Agents {
				if IsTerminal(a.State) && IsActive(a.State) {
					t.Fatalf("Invariant violated: agent %s is both terminal and active", id)
				}
			}
		}
	}
}
