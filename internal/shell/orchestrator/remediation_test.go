package orchestrator

import (
	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/git"
	logutil "agent-dashboard/internal/shell/logger"
	"agent-dashboard/internal/store"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type deliveryRuntime struct {
	*runtime.FakeRuntime
	mu         sync.Mutex
	deliveries int
	raw        string
	gate       <-chan struct{}
	opens      int
	exited     bool
}

func (r *deliveryRuntime) Name() string { return "docker" }
func (r *deliveryRuntime) Exec(ctx context.Context, h runtime.Handle, cmd []string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.deliveries++
	r.mu.Unlock()
	return io.NopCloser(strings.NewReader("")), nil
}
func (r *deliveryRuntime) Output(ctx context.Context, h runtime.Handle) (io.ReadCloser, error) {
	r.mu.Lock()
	r.opens++
	gate := r.gate
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return io.NopCloser(strings.NewReader(r.raw)), nil
}
func (r *deliveryRuntime) Inspect(ctx context.Context, h runtime.Handle) (runtime.ContainerInfo, error) {
	r.mu.Lock()
	exited := r.exited
	r.mu.Unlock()
	if exited {
		return runtime.ContainerInfo{Status: "exited", ExitCode: 0}, nil
	}
	return r.FakeRuntime.Inspect(ctx, h)
}
func seededOrchestrator(t *testing.T, raw string, cursor int64, gates ...chan struct{}) (*Orchestrator, *store.Store, *deliveryRuntime, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "shell.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if err = s.CreateRepo(ctx, &store.Repo{ID: "r", Name: "repo", Path: "/repo"}); err != nil {
		t.Fatal(err)
	}
	m := agent.NewManagerState(1)
	m.Agents["a"] = agent.Agent{ID: "a", RepoID: "r", State: agent.StateRunning, Runtime: "docker", Harness: "claude", Mode: "task", TmuxSession: "run", OutputCursor: cursor, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err = s.CommitOrchestration(ctx, 0, m, scheduler.NewState(), nil); err != nil {
		t.Fatal(err)
	}
	rt := &deliveryRuntime{FakeRuntime: runtime.NewFakeRuntime(), raw: raw}
	if len(gates) > 0 {
		rt.gate = gates[0]
	}
	if _, err = rt.Create(ctx, runtime.RuntimeSpec{SessionName: "run"}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(logutil.NewScrubbingHandler(slog.NewTextHandler(io.Discard, nil)))
	o := NewOrchestrator(s, git.NewService(t.TempDir()), rt, nil, logger, 1)
	if err = o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := o.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown orchestrator: %v", err)
		}
	})
	return o, s, rt, cancel
}

func TestFinalOutputCommitRetriesBeforeCompletion(t *testing.T) {
	raw := "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"final reply\"}]}}\n"
	gate := make(chan struct{})
	o, s, rt, _ := seededOrchestrator(t, raw, 0, gate)
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER fail_output BEFORE UPDATE ON orchestration_state BEGIN SELECT RAISE(ABORT,'injected output commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.exited = true
	rt.mu.Unlock()
	close(gate)
	// Wait until the retry opened the durable producer a second time. The exit
	// monitor has also ticked and must still be waiting for output persistence.
	waitFor(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.opens >= 2 })
	if a, _ := o.GetAgent("a"); a.State != agent.StateRunning {
		t.Fatal("exit published before final output committed")
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TRIGGER fail_output"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { a, _ := o.GetAgent("a"); return a.State == agent.StateDone })
	page, _ := s.EventsPage(ctx, "a", 0, 200)
	if len(page) != 1 || !strings.Contains(page[0].PayloadJSON, "final reply") {
		t.Fatal("final output lost or duplicated during commit retry")
	}
}
func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for durable result")
}
func TestFailedCommitDoesNotPublishOrRunEffects(t *testing.T) {
	o, s, rt, _ := seededOrchestrator(t, "", 0)
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `CREATE TRIGGER fail_transition BEFORE UPDATE ON orchestration_state BEGIN SELECT RAISE(ABORT,'injected commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := o.SendMessage(ctx, "a", "must not reach runtime"); err == nil {
		t.Fatal("failed commit reported success")
	}
	rt.mu.Lock()
	count := rt.deliveries
	rt.mu.Unlock()
	if count != 0 {
		t.Fatal("effect ran before commit")
	}
	page, err := s.EventsPage(ctx, "a", 0, 200)
	if err != nil || len(page) != 0 {
		t.Fatal("failed commit published event", err)
	}
	loaded, _, _, err := s.LoadOrchestration(ctx)
	if err != nil || loaded.Agents["a"].State != agent.StateRunning {
		t.Fatal("failed commit changed durable state", err)
	}
}
func TestStructuredSecretsScrubBeforePersistenceAndBroadcast(t *testing.T) {
	o, s, _, _ := seededOrchestrator(t, "", 0)
	ctx := context.Background()
	secret := "custom-credential-quote-\"-value"
	logutil.RegisterSecrets(o.logger, secret)
	ch, unsub := o.Broadcaster().Subscribe("a")
	defer unsub()
	ev := events.NewMessageEvent(99, time.Now().UTC(), "assistant", secret)
	ev.Payload["nested"] = map[string]any{"input": []any{secret}}
	o.mu.Lock()
	err := o.commitLocked(ctx, o.mgrState.Clone(), o.schedulerState, []agent.Command{agent.CmdPersistEvent{AgentID: "a", Event: ev}})
	o.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.EventsPage(ctx, "a", 0, 200)
	if err != nil || len(page) != 1 {
		t.Fatal(err)
	}
	if strings.Contains(page[0].PayloadJSON, "custom-credential") {
		t.Fatal("custom credential leaked into persisted nested event")
	}
	select {
	case result := <-ch:
		if strings.Contains(result.Payload["text"].(string), "custom-credential") {
			t.Fatal("custom credential leaked into live stream")
		}
	case <-time.After(time.Second):
		t.Fatal("durable event was not broadcast")
	}
}
func TestReplayCursorSurvivesRestart(t *testing.T) {
	raw := "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"session-stable\"}\n{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"new reply\"}]}}\n"
	o, s, rt, cancel := seededOrchestrator(t, raw, 1)
	waitFor(t, func() bool { page, _ := s.EventsPage(context.Background(), "a", 0, 200); return len(page) == 1 })
	a, _ := o.GetAgent("a")
	if a.OutputCursor != 2 {
		t.Fatal("producer cursor did not persist with event")
	}
	cancel()
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	o2 := NewOrchestrator(s, git.NewService(t.TempDir()), rt, nil, nil, 1)
	defer func() { _ = o2.Shutdown(context.Background()) }()
	if err := o2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	page, _ := s.EventsPage(ctx, "a", 0, 200)
	if len(page) != 1 {
		t.Fatalf("replayed harness event was duplicated: %d rows", len(page))
	}
}
func TestStartedInputHasUnknownOutcomeAndIsNotRetried(t *testing.T) {
	o, s, rt, cancel := seededOrchestrator(t, "", 0)
	cancel()
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	o.mu.Lock()
	_, err := s.CommitOrchestration(ctx, o.version, o.mgrState, o.schedulerState, []agent.Command{agent.CmdSendInput{AgentID: "a", TmuxSession: "run", Input: []byte("once")}})
	if err != nil {
		o.mu.Unlock()
		t.Fatal(err)
	}
	o.version++
	record, err := s.NextOutbox(ctx)
	if err != nil {
		o.mu.Unlock()
		t.Fatal(err)
	}
	if err = s.StartOutbox(ctx, record.ID); err != nil {
		o.mu.Unlock()
		t.Fatal(err)
	}
	o.mu.Unlock()
	o2 := NewOrchestrator(s, git.NewService(t.TempDir()), rt, nil, nil, 1)
	defer func() { _ = o2.Shutdown(context.Background()) }()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	if err = o2.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := s.NextOutbox(ctx); return err == store.ErrNotFound })
	rt.mu.Lock()
	count := rt.deliveries
	rt.mu.Unlock()
	if count != 0 {
		t.Fatalf("unknown input redelivered %d times", count)
	}
	page, _ := s.EventsPage(ctx, "a", 0, 200)
	if len(page) != 1 || !strings.Contains(page[0].PayloadJSON, "unknown outcome") {
		t.Fatal("unknown effect outcome was not recorded for user")
	}
}

func TestApprovalReplayCannotChangeAuthorizedCommand(t *testing.T) {
	o, s, _, _ := seededOrchestrator(t, "", 0)
	ctx := context.Background()
	request := agent.ApprovalRequest{ID: "approval", AgentID: "a", ToolName: "Bash", Command: "safe-command"}
	raw, _ := json.Marshal(request)
	now := time.Now().UTC()
	o.mu.Lock()
	err := o.commitLocked(ctx, o.mgrState.Clone(), o.schedulerState, []agent.Command{agent.CmdPersistApproval{ID: request.ID, AgentID: "a", Request: string(raw), Decision: "approved", DecidedAt: &now}})
	o.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	request.Command = "different-command"
	if action, err := o.RequestApproval(ctx, request); err == nil || action != "deny" {
		t.Fatal("replayed approval ID authorized a changed command")
	}
	record, err := s.GetApproval(ctx, "approval")
	if err != nil || record.Decision != "approved" {
		t.Fatal("replay changed original approval", err)
	}
}

func TestLegacyQueuedSpecIsNotInventedDuringUpgrade(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err = s.CreateRepo(ctx, &store.Repo{ID: "r", Name: "repo", Path: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateAgent(ctx, &store.AgentRecord{ID: "legacy", RepoID: "r", Harness: "claude", Runtime: "docker", Mode: "task", State: "queued", WorktreePath: "/preserved-worktree"}); err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator(s, git.NewService(t.TempDir()), runtime.NewFakeRuntime(), nil, nil, 1)
	if err = o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Shutdown(context.Background()) }()
	a, _ := o.GetAgent("legacy")
	if a.State != agent.StateFailed || a.FailureReason != "legacy_missing_runnable_spec" || a.WorktreePath != "/preserved-worktree" {
		t.Fatalf("legacy missing prompt was silently invented or worktree lost: %+v", a)
	}
	page, _ := s.EventsPage(ctx, "legacy", 0, 200)
	if len(page) != 1 || !strings.Contains(page[0].PayloadJSON, "Resubmit") {
		t.Fatal("legacy recovery limitation was not visible")
	}
}

func TestInteractiveHookSessionIdentityIsDurable(t *testing.T) {
	o, s, rt, _ := seededOrchestrator(t, "", 0)
	ctx := context.Background()
	if err := o.RecordHarnessSession(ctx, "a", "interactive-session-123"); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, err := s.LoadOrchestration(ctx)
	if err != nil || loaded.Agents["a"].HarnessSessionID != "interactive-session-123" {
		t.Fatal("interactive hook session identity was not durable", err)
	}
	if err = o.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	sessions, err := rt.ListSessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatal("dashboard shutdown destroyed resumable runtime session", err)
	}
}

type delayedCreateRuntime struct {
	*runtime.FakeRuntime
	gate chan struct{}
}

func (r *delayedCreateRuntime) Name() string { return "docker" }
func (r *delayedCreateRuntime) Create(ctx context.Context, spec runtime.RuntimeSpec) (runtime.Handle, error) {
	select {
	case <-r.gate:
		return r.FakeRuntime.Create(ctx, spec)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func TestBootReplaysCommittedSessionStartBeforeDeclaringMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "pending-start.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err = s.CreateRepo(ctx, &store.Repo{ID: "r", Name: "repo", Path: "/repo"}); err != nil {
		t.Fatal(err)
	}
	m := agent.NewManagerState(1)
	m.Agents["a"] = agent.Agent{ID: "a", RepoID: "r", State: agent.StateRunning, Runtime: "docker", Harness: "claude", Mode: "task", TmuxSession: "run", WorktreePath: "/worktree"}
	if _, err = s.CommitOrchestration(ctx, 0, m, scheduler.NewState(), []agent.Command{agent.CmdStartSession{AgentID: "a", Harness: "claude", Runtime: "docker", Mode: "task", TmuxSession: "run", WorktreePath: "/worktree"}}); err != nil {
		t.Fatal(err)
	}
	rt := &delayedCreateRuntime{FakeRuntime: runtime.NewFakeRuntime(), gate: make(chan struct{})}
	o := NewOrchestrator(s, git.NewService(t.TempDir()), rt, nil, nil, 1)
	defer func() { _ = o.Shutdown(context.Background()) }()
	if err = o.Start(ctx); err != nil {
		close(rt.gate)
		t.Fatal(err)
	}
	close(rt.gate)
	waitFor(t, func() bool { sessions, _ := rt.ListSessions(ctx); return len(sessions) == 1 })
	a, _ := o.GetAgent("a")
	if a.State != agent.StateRunning {
		t.Fatalf("committed start was mistaken for a vanished runtime: %s", a.State)
	}
}
