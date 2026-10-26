package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/core/repo"
	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/store"
)

type EventBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[chan events.NormalizedEvent]string // channel -> optional agentID filter
}

func NewEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{
		subscribers: make(map[chan events.NormalizedEvent]string),
	}
}

func (b *EventBroadcaster) Subscribe(agentID string) (chan events.NormalizedEvent, func()) {
	ch := make(chan events.NormalizedEvent, 64)
	b.mu.Lock()
	b.subscribers[ch] = agentID
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		delete(b.subscribers, ch)
		close(ch)
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

func (b *EventBroadcaster) Broadcast(agentID string, ev events.NormalizedEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch, filter := range b.subscribers {
		if filter == "" || filter == agentID {
			select {
			case ch <- ev:
			default:
				// Skip if client buffer full
			}
		}
	}
}

type SecretDecryptor interface {
	DecryptSecret(ctx context.Context, name string) (string, error)
}

type Orchestrator struct {
	mu           sync.Mutex
	mgrState     agent.ManagerState
	store        *store.Store
	gitSvc       *git.Service
	rt           runtime.Runtime
	harnesses    map[string]harness.Harness
	broadcaster  *EventBroadcaster
	decryptor    SecretDecryptor
	seqCounters  map[string]*int64
	logger       *slog.Logger
	cancelFuncs  map[string]context.CancelFunc
}

func NewOrchestrator(
	s *store.Store,
	gitSvc *git.Service,
	rt runtime.Runtime,
	decryptor SecretDecryptor,
	logger *slog.Logger,
	runningCap int,
) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	claudeAdapter := claude.NewAdapter()
	return &Orchestrator{
		mgrState:    agent.NewManagerState(runningCap),
		store:       s,
		gitSvc:      gitSvc,
		rt:          rt,
		harnesses:   map[string]harness.Harness{"claude": claudeAdapter},
		broadcaster: NewEventBroadcaster(),
		decryptor:   decryptor,
		seqCounters: make(map[string]*int64),
		logger:      logger,
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

func (o *Orchestrator) Broadcaster() *EventBroadcaster {
	return o.broadcaster
}

func (o *Orchestrator) Runtime() runtime.Runtime {
	return o.rt
}

// Start boots the orchestrator, loads state from DB, checks running tmux sessions, and reconciles.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	// 1. Load agents from database
	records, err := o.store.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("failed to load agents from store: %w", err)
	}

	for _, rec := range records {
		a := agent.Agent{
			ID:               rec.ID,
			RepoID:           rec.RepoID,
			Harness:          rec.Harness,
			Model:            rec.Model,
			Runtime:          rec.Runtime,
			Mode:             rec.Mode,
			Branch:           rec.Branch,
			WorktreePath:     rec.WorktreePath,
			TmuxSession:      rec.TmuxSession,
			HarnessSessionID: rec.HarnessSessionID,
			State:            agent.State(rec.State),
			CreatedAt:        rec.CreatedAt,
			UpdatedAt:        rec.UpdatedAt,
		}
		// Resolve repo path
		if r, err := o.store.GetRepo(ctx, rec.RepoID); err == nil {
			a.RepoPath = r.Path
		}
		o.mgrState.Agents[a.ID] = a
		if a.State == agent.StateQueued {
			o.mgrState.Queue = append(o.mgrState.Queue, a.ID)
		}
	}

	// 2. Discover running tmux sessions
	activeSessions, err := o.rt.ListSessions(ctx)
	if err != nil {
		o.logger.Warn("could not list runtime sessions on boot", "error", err)
	}
	sessionSet := make(map[string]bool)
	for _, s := range activeSessions {
		sessionSet[s] = true
	}

	// 3. Step reconcile input through pure state machine
	var cmds []agent.Command
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputReconcileBoot{
		Now:                 time.Now().UTC(),
		RunningTmuxSessions: sessionSet,
	})
	if err != nil {
		return fmt.Errorf("reconciliation step error: %w", err)
	}

	// 4. Execute commands from reconciliation
	go o.executeCommands(context.Background(), cmds)

	o.logger.Info("orchestrator initialized",
		"loaded_agents", len(records),
		"active_tmux", len(activeSessions),
		"active_agents", o.mgrState.ActiveCount(),
	)
	return nil
}

// SubmitAgent handles user request to create and run an agent.
func (o *Orchestrator) SubmitAgent(ctx context.Context, repoID, harnessName, model, mode, prompt, baseBranch, customBranch string) (*agent.Agent, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	r, err := o.store.GetRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("repo %q not found: %w", repoID, err)
	}

	if harnessName == "" {
		harnessName = "claude"
	}
	if _, ok := o.harnesses[harnessName]; !ok {
		return nil, fmt.Errorf("unsupported harness %q", harnessName)
	}

	if baseBranch == "" {
		baseBranch = r.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}

	agentID := fmt.Sprintf("agent-%d", time.Now().UnixNano())
	slug := repo.SanitizeSlug(prompt)
	if customBranch != "" {
		slug = repo.SanitizeSlug(customBranch)
	}
	branch := fmt.Sprintf("agent/%s", slug)
	worktreePath := filepath.Join(o.gitSvc.WorkspaceDir(), fmt.Sprintf("%s-wt-%s", r.Name, slug))
	tmuxSession := fmt.Sprintf("ad-%s", agentID)

	spec := agent.AgentSpec{
		ID:           agentID,
		RepoID:       r.ID,
		RepoPath:     r.Path,
		Harness:      harnessName,
		Model:        model,
		Runtime:      "host",
		Mode:         mode,
		Prompt:       prompt,
		Branch:       branch,
		BaseBranch:   baseBranch,
		WorktreePath: worktreePath,
		TmuxSession:  tmuxSession,
	}

	var cmds []agent.Command
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputSubmitAgent{
		Now:  time.Now().UTC(),
		Spec: spec,
	})
	if err != nil {
		return nil, err
	}

	a := o.mgrState.Agents[agentID]
	go o.executeCommands(context.Background(), cmds)
	return &a, nil
}

// SendMessage sends user text or follow-up prompt to an agent.
func (o *Orchestrator) SendMessage(ctx context.Context, agentID, message string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputSendMessage{
		Now:     time.Now().UTC(),
		AgentID: agentID,
		Message: message,
	})
	if err != nil {
		return err
	}
	go o.executeCommands(context.Background(), cmds)
	return nil
}

// StopAgent stops an agent and frees its concurrency slot.
func (o *Orchestrator) StopAgent(ctx context.Context, agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputStopRequested{
		Now:     time.Now().UTC(),
		AgentID: agentID,
	})
	if err != nil {
		return err
	}

	if cancel, ok := o.cancelFuncs[agentID]; ok {
		cancel()
		delete(o.cancelFuncs, agentID)
	}

	go o.executeCommands(context.Background(), cmds)
	return nil
}

// DeleteAgent removes the agent, kills tmux, and cleans up the worktree.
func (o *Orchestrator) DeleteAgent(ctx context.Context, agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputDeleteRequested{
		Now:     time.Now().UTC(),
		AgentID: agentID,
	})
	if err != nil {
		return err
	}

	if cancel, ok := o.cancelFuncs[agentID]; ok {
		cancel()
		delete(o.cancelFuncs, agentID)
	}

	go o.executeCommands(context.Background(), cmds)
	return nil
}

// GetAgent returns the current in-memory agent state.
func (o *Orchestrator) GetAgent(agentID string) (agent.Agent, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	a, ok := o.mgrState.Agents[agentID]
	return a, ok
}

// ListAgents returns all agents.
func (o *Orchestrator) ListAgents() []agent.Agent {
	o.mu.Lock()
	defer o.mu.Unlock()
	list := make([]agent.Agent, 0, len(o.mgrState.Agents))
	for _, a := range o.mgrState.Agents {
		list = append(list, a)
	}
	return list
}

func (o *Orchestrator) executeCommands(ctx context.Context, cmds []agent.Command) {
	for _, cmd := range cmds {
		switch c := cmd.(type) {
		case agent.CmdPersistAgentState:
			rec := &store.AgentRecord{
				ID:               c.Agent.ID,
				RepoID:           c.Agent.RepoID,
				Harness:          c.Agent.Harness,
				Model:            c.Agent.Model,
				Runtime:          c.Agent.Runtime,
				Mode:             c.Agent.Mode,
				Branch:           c.Agent.Branch,
				WorktreePath:     c.Agent.WorktreePath,
				TmuxSession:      c.Agent.TmuxSession,
				HarnessSessionID: c.Agent.HarnessSessionID,
				State:            string(c.Agent.State),
			}
			if err := o.store.CreateAgent(ctx, rec); err != nil {
				o.logger.Error("failed to persist agent state", "agent", rec.ID, "error", err)
			}

		case agent.CmdCreateWorktree:
			go func(cc agent.CmdCreateWorktree) {
				err := o.gitSvc.CreateWorktree(ctx, cc.RepoPath, cc.BaseBranch, cc.Branch, cc.WorktreePath)
				o.mu.Lock()
				defer o.mu.Unlock()
				var nextCmds []agent.Command
				var stepErr error
				if err != nil {
					o.logger.Error("failed to create worktree", "agent", cc.AgentID, "error", err)
					o.mgrState, nextCmds, stepErr = agent.Step(o.mgrState, agent.InputWorktreeFailed{
						Now:     time.Now().UTC(),
						AgentID: cc.AgentID,
						Error:   err.Error(),
					})
				} else {
					o.mgrState, nextCmds, stepErr = agent.Step(o.mgrState, agent.InputWorktreeReady{
						Now:          time.Now().UTC(),
						AgentID:      cc.AgentID,
						WorktreePath: cc.WorktreePath,
					})
				}
				if stepErr == nil && len(nextCmds) > 0 {
					go o.executeCommands(ctx, nextCmds)
				}
			}(c)

		case agent.CmdStartSession:
			go o.startAgentSession(ctx, c)

		case agent.CmdSendInput:
			_, _ = o.rt.Exec(ctx, runtime.Handle(c.TmuxSession), []string{string(c.Input)})

		case agent.CmdKillSession:
			_ = o.rt.Destroy(ctx, runtime.Handle(c.TmuxSession))

		case agent.CmdRemoveWorktree:
			go func(cc agent.CmdRemoveWorktree) {
				_ = o.gitSvc.RemoveWorktree(ctx, cc.RepoPath, cc.WorktreePath, cc.Branch)
			}(c)

		case agent.CmdDeleteAgentRecord:
			_ = o.store.DeleteAgent(ctx, c.AgentID)

		case agent.CmdPersistEvent:
			payloadBytes, _ := json.Marshal(c.Event.Payload)
			_ = o.store.AppendEvent(ctx, c.AgentID, c.Event.Seq, string(c.Event.Type), string(payloadBytes))
			o.broadcaster.Broadcast(c.AgentID, c.Event)

		case agent.CmdNotifyUser:
			o.logger.Info("push notification", "title", c.Title, "body", c.Body, "url", c.TargetURL)
		}
	}
}

func (o *Orchestrator) startAgentSession(ctx context.Context, c agent.CmdStartSession) {
	harn, ok := o.harnesses[c.Harness]
	if !ok {
		o.failAgent(c.AgentID, fmt.Sprintf("unknown harness %s", c.Harness))
		return
	}

	spec := agent.AgentSpec{
		ID:           c.AgentID,
		Mode:         c.Mode,
		Model:        c.Model,
		Prompt:       c.Prompt,
		WorktreePath: c.WorktreePath,
		TmuxSession:  c.TmuxSession,
	}

	var cmdArgs []string
	if c.ResumeID != "" {
		cmdArgs = harn.ResumeCommand(spec, c.ResumeID)
	} else {
		cmdArgs = harn.StartCommand(spec)
	}

	// Fetch credentials if available
	env := make(map[string]string)
	if o.decryptor != nil {
		if token, err := o.decryptor.DecryptSecret(ctx, "claude_token"); err == nil && token != "" {
			env["ANTHROPIC_AUTH_TOKEN"] = token
		} else if apiKey, err := o.decryptor.DecryptSecret(ctx, "anthropic_api_key"); err == nil && apiKey != "" {
			env["ANTHROPIC_API_KEY"] = apiKey
		}
	}

	// Create runtime session in tmux
	rtSpec := runtime.RuntimeSpec{
		SessionName: c.TmuxSession,
		WorkDir:     c.WorktreePath,
		Command:     cmdArgs,
		Env:         env,
	}

	_, err := o.rt.Create(ctx, rtSpec)
	if err != nil {
		o.failAgent(c.AgentID, fmt.Sprintf("failed to spawn runtime session: %v", err))
		return
	}

	agentCtx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancelFuncs[c.AgentID] = cancel
	o.mu.Unlock()

	// If task mode, attach reader to parse output events
	if c.Mode == "task" {
		go o.consumeTaskOutput(agentCtx, c.AgentID, c.TmuxSession, harn)
	}
}

func (o *Orchestrator) consumeTaskOutput(ctx context.Context, agentID, tmuxSession string, harn harness.Harness) {
	reader, err := o.rt.Exec(ctx, runtime.Handle(tmuxSession), nil)
	if err != nil {
		return
	}
	defer reader.Close()

	evCh, errCh := harn.ParseEvents(ctx, reader)
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errCh:
			if ok && err != nil && err != io.EOF {
				o.logger.Error("harness parse error", "agent", agentID, "error", err)
			}
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			o.mu.Lock()
			var nextCmds []agent.Command
			var stepErr error
			o.mgrState, nextCmds, stepErr = agent.Step(o.mgrState, agent.InputHarnessEvent{
				Now:     time.Now().UTC(),
				AgentID: agentID,
				Event:   ev,
			})
			o.mu.Unlock()
			if stepErr == nil && len(nextCmds) > 0 {
				go o.executeCommands(ctx, nextCmds)
			}
		}
	}
}

func (o *Orchestrator) failAgent(agentID, errStr string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mgrState, _, _ = agent.Step(o.mgrState, agent.InputProcessExited{
		Now:      time.Now().UTC(),
		AgentID:  agentID,
		ExitCode: 1,
	})
	ev := events.NewErrorEvent(1, time.Now().UTC(), errStr, nil)
	payloadBytes, _ := json.Marshal(ev.Payload)
	_ = o.store.AppendEvent(context.Background(), agentID, 1, string(ev.Type), string(payloadBytes))
	o.broadcaster.Broadcast(agentID, ev)
}
