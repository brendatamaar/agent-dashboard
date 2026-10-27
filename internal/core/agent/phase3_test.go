package agent_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-dashboard/internal/core/agent"
)

func TestPerRepoConcurrencyCapAndQueue(t *testing.T) {
	now := time.Now().UTC()
	// Global cap = 2, per-repo cap = 1
	mgr := agent.NewManagerState(2, 1)

	spec1 := agent.AgentSpec{
		ID:           "a-1",
		RepoID:       "repo-A",
		RepoPath:     "/workspace/repo-A",
		Harness:      "claude",
		Runtime:      "docker",
		WorktreePath: "/workspace/repo-A/wt-1",
	}
	spec2 := agent.AgentSpec{
		ID:           "a-2",
		RepoID:       "repo-A", // Same repo!
		RepoPath:     "/workspace/repo-A",
		Harness:      "claude",
		Runtime:      "docker",
		WorktreePath: "/workspace/repo-A/wt-2",
	}
	spec3 := agent.AgentSpec{
		ID:           "a-3",
		RepoID:       "repo-B", // Different repo!
		RepoPath:     "/workspace/repo-B",
		Harness:      "claude",
		Runtime:      "docker",
		WorktreePath: "/workspace/repo-B/wt-3",
	}

	// 1. Submit a-1 on repo-A -> should provision
	var err error
	mgr, _, err = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec1})
	if err != nil {
		t.Fatalf("submit a-1 failed: %v", err)
	}
	if mgr.Agents["a-1"].State != agent.StateProvisioning {
		t.Fatalf("expected a-1 provisioning, got %s", mgr.Agents["a-1"].State)
	}

	// 2. Submit a-2 on repo-A -> global cap has space (1/2), but repo-A is at capacity (1/1) -> should QUEUE
	mgr, _, err = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec2})
	if err != nil {
		t.Fatalf("submit a-2 failed: %v", err)
	}
	if mgr.Agents["a-2"].State != agent.StateQueued {
		t.Fatalf("expected a-2 queued due to repo-A cap, got %s", mgr.Agents["a-2"].State)
	}
	if len(mgr.Queue) != 1 || mgr.Queue[0] != "a-2" {
		t.Fatalf("expected a-2 in queue, got %v", mgr.Queue)
	}

	// 3. Submit a-3 on repo-B -> repo-B is at 0/1 and global is at 1/2 -> should PROVISION
	mgr, _, err = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec3})
	if err != nil {
		t.Fatalf("submit a-3 failed: %v", err)
	}
	if mgr.Agents["a-3"].State != agent.StateProvisioning {
		t.Fatalf("expected a-3 provisioning, got %s", mgr.Agents["a-3"].State)
	}
	// Global cap is now full (a-1 and a-3 are provisioning/active)
	if mgr.ActiveCount() != 2 {
		t.Fatalf("expected 2 active agents, got %d", mgr.ActiveCount())
	}

	// 4. Finish a-1 on repo-A -> slot on repo-A frees -> a-2 should automatically dequeue and provision
	mgr, _, err = agent.Step(mgr, agent.InputProcessExited{Now: now, AgentID: "a-1", ExitCode: 0})
	if err != nil {
		t.Fatalf("process exit a-1 failed: %v", err)
	}
	if mgr.Agents["a-1"].State != agent.StateDone {
		t.Fatalf("expected a-1 done, got %s", mgr.Agents["a-1"].State)
	}
	if mgr.Agents["a-2"].State != agent.StateProvisioning {
		t.Fatalf("expected a-2 dequeued and provisioning, got %s", mgr.Agents["a-2"].State)
	}
	if len(mgr.Queue) != 0 {
		t.Fatalf("expected empty queue, got %v", mgr.Queue)
	}
}

func TestOOMFailureDetection(t *testing.T) {
	now := time.Now().UTC()
	mgr := agent.NewManagerState(2)

	spec := agent.AgentSpec{
		ID:           "oom-agent",
		RepoID:       "repo-1",
		RepoPath:     "/workspace/repo-1",
		Harness:      "claude",
		Runtime:      "docker",
		WorktreePath: "/workspace/repo-1/wt-oom",
	}

	mgr, _, _ = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec})
	mgr, _, _ = agent.Step(mgr, agent.InputWorktreeReady{Now: now, AgentID: spec.ID, WorktreePath: spec.WorktreePath})

	// Simulate OOM kill with ExitCode 137 and OOMKilled=true
	var cmds []agent.Command
	var err error
	mgr, cmds, err = agent.Step(mgr, agent.InputProcessExited{
		Now:       now,
		AgentID:   spec.ID,
		ExitCode:  137,
		OOMKilled: true,
	})
	if err != nil {
		t.Fatalf("InputProcessExited error: %v", err)
	}

	a := mgr.Agents[spec.ID]
	if a.State != agent.StateFailed {
		t.Fatalf("expected StateFailed, got %s", a.State)
	}
	if a.FailureReason != "oom_killed" {
		t.Fatalf("expected FailureReason 'oom_killed', got %q", a.FailureReason)
	}

	// Verify notification has OOM details
	hasOOMNotify := false
	for _, c := range cmds {
		if nc, ok := c.(agent.CmdNotifyUser); ok {
			if nc.Title == "Agent failed (OOM): oom-agent" {
				hasOOMNotify = true
			}
		}
	}
	if !hasOOMNotify {
		t.Fatalf("expected OOM notification, but none found in %v", cmds)
	}
}

func TestAutoApproveSecurityGuard(t *testing.T) {
	now := time.Now().UTC()
	mgr := agent.NewManagerState(2)

	// 1. AutoApprove on host runtime must be rejected
	badSpec := agent.AgentSpec{
		ID:          "host-agent",
		RepoID:      "repo-1",
		Runtime:     "host",
		AutoApprove: true,
	}
	_, _, err := agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: badSpec})
	if err == nil {
		t.Fatalf("expected error when AutoApprove is requested on host runtime, got nil")
	}

	// 2. AutoApprove on docker runtime is allowed
	goodSpec := agent.AgentSpec{
		ID:          "docker-agent",
		RepoID:      "repo-1",
		Runtime:     "docker",
		AutoApprove: true,
	}
	mgr, _, err = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: goodSpec})
	if err != nil {
		t.Fatalf("unexpected error for docker auto-approve: %v", err)
	}
	if !mgr.Agents["docker-agent"].AutoApprove {
		t.Fatalf("expected AutoApprove true on docker agent")
	}
}

func TestCancelQueuedAgent(t *testing.T) {
	now := time.Now().UTC()
	mgr := agent.NewManagerState(1) // cap = 1

	spec1 := agent.AgentSpec{ID: "run-1", RepoID: "repo-1", Runtime: "docker"}
	spec2 := agent.AgentSpec{ID: "queue-1", RepoID: "repo-1", Runtime: "docker"}

	mgr, _, _ = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec1})
	mgr, _, _ = agent.Step(mgr, agent.InputSubmitAgent{Now: now, Spec: spec2})

	if mgr.Agents["queue-1"].State != agent.StateQueued {
		t.Fatalf("expected queue-1 queued, got %s", mgr.Agents["queue-1"].State)
	}

	// Cancel queued agent
	var err error
	mgr, _, err = agent.Step(mgr, agent.InputCancelQueued{Now: now, AgentID: "queue-1"})
	if err != nil {
		t.Fatalf("cancel queued agent failed: %v", err)
	}

	if mgr.Agents["queue-1"].State != agent.StateStopped {
		t.Fatalf("expected queue-1 stopped, got %s", mgr.Agents["queue-1"].State)
	}
	if len(mgr.Queue) != 0 {
		t.Fatalf("expected queue to be empty after cancel, got %v", mgr.Queue)
	}
}

func TestAgentJSONSerialization(t *testing.T) {
	a := agent.Agent{
		ID:            "agent-123",
		RepoID:        "repo-456",
		State:         agent.StateQueued,
		FailureReason: "oom_killed",
		AutoApprove:   true,
	}

	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	js := string(b)
	if !strings.Contains(js, `"id":"agent-123"`) {
		t.Fatalf("expected JSON to contain '\"id\":\"agent-123\"', got %s", js)
	}
	if !strings.Contains(js, `"repo_id":"repo-456"`) {
		t.Fatalf("expected JSON to contain '\"repo_id\":\"repo-456\"', got %s", js)
	}
	if !strings.Contains(js, `"state":"queued"`) {
		t.Fatalf("expected JSON to contain '\"state\":\"queued\"', got %s", js)
	}
}
