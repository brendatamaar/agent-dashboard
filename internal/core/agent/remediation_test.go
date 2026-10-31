package agent

import (
	"agent-dashboard/internal/core/events"
	"math/rand"
	"testing"
	"time"
)

func readyAgent(t *testing.T, s ManagerState, id, repo string) ManagerState {
	t.Helper()
	var err error
	s, _, err = Step(s, InputSubmitAgent{Spec: AgentSpec{ID: id, RepoID: repo, Runtime: "docker", Mode: "task", TmuxSession: id, WorktreePath: "/" + id}, Now: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	s, _, err = Step(s, InputWorktreeReady{AgentID: id, WorktreePath: "/" + id})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestApprovalOwnershipAndPerAgentCount(t *testing.T) {
	s := readyAgent(t, NewManagerState(2), "a", "r1")
	s = readyAgent(t, s, "b", "r2")
	for _, r := range []ApprovalRequest{{ID: "a1", AgentID: "a"}, {ID: "a2", AgentID: "a"}, {ID: "b1", AgentID: "b"}} {
		var err error
		s, _, err = Step(s, InputApprovalRequest{Request: r})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, cmds, err := Step(s, InputApprovalDecision{AgentID: "b", ApprovalID: "a1", Approved: true}); err == nil || len(cmds) != 0 {
		t.Fatal("approval ownership: cross-agent decision accepted")
	}
	s, _, _ = Step(s, InputApprovalDecision{AgentID: "a", ApprovalID: "a1", Approved: true})
	if s.Agents["a"].State != StateAwaitingApproval {
		t.Fatal("per-agent approval count: second approval was lost")
	}
	s, _, _ = Step(s, InputApprovalDecision{AgentID: "a", ApprovalID: "a2", Approved: true})
	if s.Agents["a"].State != StateRunning || s.Agents["b"].State != StateAwaitingApproval {
		t.Fatal("another agent's pending approval blocked resolved agent")
	}
	s, _, _ = Step(s, InputStopRequested{AgentID: "a"})
	if _, cmds, err := Step(s, InputApprovalRequest{Request: ApprovalRequest{ID: "late", AgentID: "a"}}); err == nil || len(cmds) != 0 {
		t.Fatal("late approval resurrected a stopped agent")
	}
}
func TestPhysicalSlotHeldUntilStopAcknowledged(t *testing.T) {
	s := readyAgent(t, NewManagerState(1), "a", "repo")
	s, _, _ = Step(s, InputSubmitAgent{Spec: AgentSpec{ID: "b", RepoID: "repo"}})
	s, cmds, err := Step(s, InputStopRequested{AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveCount() != 1 || s.Agents["b"].State != StateQueued {
		t.Fatal("physical concurrency: queued agent started before destroy completed")
	}
	found := false
	for _, cmd := range cmds {
		if kill, ok := cmd.(CmdKillSession); ok {
			found = true
			if kill.Runtime != "docker" {
				t.Fatal("kill command lost runtime ownership")
			}
		}
	}
	if !found {
		t.Fatal("missing kill command")
	}
	s, _, _ = Step(s, InputSessionStopped{AgentID: "a"})
	if s.Agents["b"].State != StateProvisioning {
		t.Fatal("slot was not released after acknowledgment")
	}
	if _, cmds, err = Step(s, InputWorktreeReady{AgentID: "a"}); err != nil || len(cmds) != 0 {
		t.Fatal("late provisioning result emitted effects after cancellation")
	}
}
func TestResumePreservesWorktreeAndHarnessSession(t *testing.T) {
	s := readyAgent(t, NewManagerState(1), "a", "repo")
	a := s.Agents["a"]
	a.HarnessSessionID = "harness-session"
	s.Agents["a"] = a
	s, _, _ = Step(s, InputProcessExited{AgentID: "a", ExitCode: 0})
	s, _, err := Step(s, InputResumeRequested{AgentID: "a", TmuxSession: "new-run"})
	if err != nil {
		t.Fatal(err)
	}
	a = s.Agents["a"]
	if a.WorktreePath != "/a" || a.HarnessSessionID != "harness-session" || a.TmuxSession != "new-run" || !a.Resuming {
		t.Fatalf("resume identity: %+v", a)
	}
}
func TestDoneReservesSlotUntilPhysicalExit(t *testing.T) {
	s := readyAgent(t, NewManagerState(1), "a", "r")
	s, _, _ = Step(s, InputSubmitAgent{Spec: AgentSpec{ID: "b", RepoID: "r"}})
	s, _, _ = Step(s, InputHarnessEvent{AgentID: "a", Event: events.NewDoneEvent(1, time.Time{}, "done", "")})
	if s.ActiveCount() != 1 || s.Agents["b"].State != StateQueued {
		t.Fatal("done event freed capacity before actual process stop")
	}
}

func TestPropertyPhysicalCapacityAndApprovalOwnership(t *testing.T) {
	r := rand.New(rand.NewSource(81027))
	for trial := 0; trial < 100; trial++ {
		var sequence []Input
		for i := 0; i < 100; i++ {
			id := []string{"a", "b", "c"}[r.Intn(3)]
			approval := []string{"one", "two"}[r.Intn(2)]
			var in Input
			switch r.Intn(9) {
			case 0:
				in = InputSubmitAgent{Spec: AgentSpec{ID: id, RepoID: "r", Runtime: "docker", TmuxSession: id, Mode: "task"}}
			case 1:
				in = InputWorktreeReady{AgentID: id}
			case 2:
				in = InputApprovalRequest{Request: ApprovalRequest{ID: approval, AgentID: id}}
			case 3:
				in = InputApprovalDecision{AgentID: id, ApprovalID: approval, Approved: true}
			case 4:
				in = InputStopRequested{AgentID: id}
			case 5:
				in = InputSessionStopped{AgentID: id}
			case 6:
				in = InputProcessExited{AgentID: id}
			case 7:
				in = InputDeleteRequested{AgentID: id}
			case 8:
				in = InputHarnessEvent{AgentID: id, Event: events.NewDoneEvent(0, time.Time{}, "", "")}
			}
			sequence = append(sequence, in)
		}
		check := func(seq []Input) bool {
			s := NewManagerState(1)
			for _, in := range seq {
				next, cmds, err := Step(s, in)
				if err == nil {
					s = next
				}
				if s.ActiveCount() > 1 || s.ActiveCountForRepo("r") > 1 {
					return false
				}
				for _, req := range s.PendingApprovals {
					a, ok := s.Agents[req.AgentID]
					if !ok || a.State != StateAwaitingApproval {
						return false
					}
				}
				for _, cmd := range cmds {
					if start, ok := cmd.(CmdStartSession); ok {
						a, exists := s.Agents[start.AgentID]
						if !exists || a.State != StateRunning {
							return false
						}
					}
				}
			}
			return true
		}
		if !check(sequence) {
			for i := 0; i < len(sequence); {
				candidate := append(append([]Input{}, sequence[:i]...), sequence[i+1:]...)
				if !check(candidate) {
					sequence = candidate
					i = 0
				} else {
					i++
				}
			}
			t.Fatalf("physical capacity/approval ownership failed; minimized inputs: %#v", sequence)
		}
	}
}
