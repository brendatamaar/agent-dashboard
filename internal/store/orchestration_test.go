package store_test

import (
	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/store"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func durableStore(t *testing.T) (*store.Store, agent.ManagerState) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "durable.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.CreateRepo(ctx, &store.Repo{ID: "r", Name: "repo", Path: "/repo"}); err != nil {
		t.Fatal(err)
	}
	m := agent.NewManagerState(1)
	m.Agents["a"] = agent.Agent{ID: "a", RepoID: "r", RepoPath: "/repo", Prompt: "original prompt", BaseBranch: "main", State: agent.StateQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), EnvVars: map[string]string{"EDITOR": "vim"}}
	m.Queue = []string{"a"}
	return s, m
}
func TestDurableTransitionContract(t *testing.T) {
	ctx := context.Background()
	s, m := durableStore(t)
	sc := scheduler.NewState()
	cmds := []agent.Command{agent.CmdPersistEvent{AgentID: "a", Event: events.NewMessageEvent(0, time.Now().UTC(), "user", "hello")}, agent.CmdSendInput{AgentID: "a", TmuxSession: "run", Input: []byte("hello")}}
	emitted, err := s.CommitOrchestration(ctx, 0, m, sc, cmds)
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 || emitted[0].Seq != 1 {
		t.Fatal("monotonic cursor missing")
	}
	loaded, _, version, err := s.LoadOrchestration(ctx)
	if err != nil || version != 1 || loaded.Agents["a"].Prompt != "original prompt" || loaded.Queue[0] != "a" {
		t.Fatal("complete runnable spec/FIFO did not round trip", err)
	}
	// A late SQL failure rolls back the version, events and outbox together.
	bad := append(append([]agent.Command{}, cmds...), agent.CmdPersistApproval{ID: "bad", AgentID: "unknown", Request: "{}", Decision: "pending"})
	if _, err = s.CommitOrchestration(ctx, 1, loaded, sc, bad); err == nil {
		t.Fatal("foreign-key failure was swallowed")
	}
	_, _, afterFailure, err := s.LoadOrchestration(ctx)
	if err != nil || afterFailure != 1 {
		t.Fatal("failed transaction advanced version", err)
	}
	m.Agents["a"] = agent.Agent{ID: "a", RepoID: "missing"}
	if _, err = s.CommitOrchestration(ctx, 0, m, sc, cmds); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale version did not conflict", err)
	}
	page, err := s.EventsPage(ctx, "a", 0, 500)
	if err != nil || len(page) != 1 {
		t.Fatal("failed commit left event rows", err)
	}
	r, err := s.NextOutbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StartOutbox(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.NextOutbox(ctx)
	if r2.Status != "started" {
		t.Fatal("unknown command status not durable")
	}
	if err = s.CompleteOutbox(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.NextOutbox(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("completed effect redelivered", err)
	}
	loaded.Agents["a"] = agent.Agent{ID: "a", RepoID: "r", State: agent.StateRunning}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.CommitOrchestration(ctx, 1, loaded, sc, nil)
			if e == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(e, store.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one concurrent writer must win, got %d", wins)
	}
}

func TestCompletedOutboxRetentionPreservesUnfinishedEffects(t *testing.T) {
	ctx := context.Background()
	s, m := durableStore(t)
	sc := scheduler.NewState()
	commands := []agent.Command{agent.CmdSendInput{AgentID: "a", Input: []byte("pending")}, agent.CmdSendInput{AgentID: "a", Input: []byte("started")}}
	if _, err := s.CommitOrchestration(ctx, 0, m, sc, commands); err != nil {
		t.Fatal(err)
	}
	if err := s.StartOutbox(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO orchestration_outbox(id,kind,payload_json,status) VALUES(3,'agent.CmdNotifyUser','{}','done'),(10004,'agent.CmdNotifyUser','{}','pending')`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteOutbox(ctx, 10004); err != nil {
		t.Fatal(err)
	}
	var oldDone, unfinished int
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM orchestration_outbox WHERE id=3").Scan(&oldDone); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM orchestration_outbox WHERE id IN (1,2) AND status!='done'").Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if oldDone != 0 || unfinished != 2 {
		t.Fatalf("retention removed unfinished work or kept old completed work: done=%d unfinished=%d", oldDone, unfinished)
	}
}
func TestEventSequencePagingAndRetention(t *testing.T) {
	ctx := context.Background()
	s, m := durableStore(t)
	sc := scheduler.NewState()
	cmds := make([]agent.Command, 10005)
	for i := range cmds {
		cmds[i] = agent.CmdPersistEvent{AgentID: "a", Event: events.NewMessageEvent(0, time.Now().UTC(), "user", "x")}
	}
	if _, err := s.CommitOrchestration(ctx, 0, m, sc, cmds); err != nil {
		t.Fatal(err)
	}
	page, err := s.EventsPage(ctx, "a", 0, 9000)
	if err != nil || len(page) != 500 || page[0].Seq != 6 {
		t.Fatalf("bounded retention/page: len=%d err=%v", len(page), err)
	}
	latest, err := s.CommitOrchestration(ctx, 1, m, sc, []agent.Command{agent.CmdPersistEvent{AgentID: "a", Event: events.NewMessageEvent(0, time.Now().UTC(), "user", "after restart")}})
	if err != nil || latest[0].Seq != 10006 {
		t.Fatal("durable cursor regressed after retention", err)
	}
}
func TestInitialUserAtomicSingleton(t *testing.T) {
	ctx := context.Background()
	s, _ := durableStore(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := s.CreateInitialUser(ctx, &store.User{ID: id, Username: id, MasterSalt: []byte("salt")})
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, store.ErrUserExists) {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("single administrator invariant: %d users created", wins)
	}
}
