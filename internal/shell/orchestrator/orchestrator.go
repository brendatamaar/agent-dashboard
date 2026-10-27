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
	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/harness/codex"
	"agent-dashboard/internal/harness/gemini"
	"agent-dashboard/internal/harness/muse"
	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/git"
	"agent-dashboard/internal/shell/notifier"
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
	mu                 sync.Mutex
	mgrState           agent.ManagerState
	store              *store.Store
	gitSvc             *git.Service
	notifier           *notifier.Notifier
	rt                 runtime.Runtime
	runtimes           map[string]runtime.Runtime
	harnesses          map[string]harness.Harness
	harnessRegistry    *harness.Registry
	schedulerState     scheduler.State
	scheduledRuns      map[string]string
	broadcaster        *EventBroadcaster
	decryptor          SecretDecryptor
	seqCounters        map[string]*int64
	logger             *slog.Logger
	cancelFuncs        map[string]context.CancelFunc
	pendingResolutions map[string]chan string
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
	codexAdapter := codex.NewAdapter()
	geminiAdapter := gemini.NewAdapter()
	museAdapter := muse.NewAdapter()

	harnessReg := harness.NewRegistry()
	harnessReg.Register(claudeAdapter)
	harnessReg.Register(codexAdapter)
	harnessReg.Register(geminiAdapter)
	harnessReg.Register(museAdapter)

	harnesses := map[string]harness.Harness{
		"claude": claudeAdapter,
		"codex":  codexAdapter,
		"gemini": geminiAdapter,
		"muse":   museAdapter,
	}
	runtimes := make(map[string]runtime.Runtime)
	if rt != nil {
		runtimes[rt.Name()] = rt
	}
	return &Orchestrator{
		mgrState:           agent.NewManagerState(runningCap),
		store:              s,
		gitSvc:             gitSvc,
		rt:                 rt,
		runtimes:           runtimes,
		harnesses:          harnesses,
		harnessRegistry:    harnessReg,
		schedulerState:     scheduler.NewState(),
		scheduledRuns:      make(map[string]string),
		broadcaster:        NewEventBroadcaster(),
		decryptor:          decryptor,
		seqCounters:        make(map[string]*int64),
		logger:             logger,
		cancelFuncs:        make(map[string]context.CancelFunc),
		pendingResolutions: make(map[string]chan string),
	}
}

func (o *Orchestrator) RegisterRuntime(name string, rt runtime.Runtime) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.runtimes == nil {
		o.runtimes = make(map[string]runtime.Runtime)
	}
	o.runtimes[name] = rt
}

func (o *Orchestrator) getRuntime(name string) runtime.Runtime {
	if o.runtimes != nil {
		if r, ok := o.runtimes[name]; ok {
			return r
		}
	}
	if o.rt != nil {
		return o.rt
	}
	if o.runtimes != nil {
		if r, ok := o.runtimes["docker"]; ok {
			return r
		}
		if r, ok := o.runtimes["host"]; ok {
			return r
		}
	}
	return nil
}

func (o *Orchestrator) SetNotifier(n *notifier.Notifier) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.notifier = n
}

func (o *Orchestrator) GitService() *git.Service {
	return o.gitSvc
}

func (o *Orchestrator) Notifier() *notifier.Notifier {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.notifier
}

func (o *Orchestrator) Broadcaster() *EventBroadcaster {
	return o.broadcaster
}

func (o *Orchestrator) Runtime(name ...string) runtime.Runtime {
	if len(name) > 0 {
		return o.getRuntime(name[0])
	}
	return o.getRuntime("docker")
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
			FailureReason:    rec.FailureReason,
			AutoApprove:      rec.AutoApprove,
			CreatedAt:        rec.CreatedAt,
			UpdatedAt:        rec.UpdatedAt,
		}
		o.mgrState.Agents[a.ID] = a
		if a.State == agent.StateQueued {
			o.mgrState.Queue = append(o.mgrState.Queue, a.ID)
		}
	}

	// 2. Query running sessions
	runningSessions := make(map[string]bool)
	rt := o.getRuntime("host")
	if rt != nil {
		sessions, err := rt.ListSessions(ctx)
		if err == nil {
			for _, s := range sessions {
				runningSessions[s] = true
			}
		}
	}
	docRt := o.getRuntime("docker")
	if docRt != nil {
		sessions, err := docRt.ListSessions(ctx)
		if err == nil {
			for _, s := range sessions {
				runningSessions[s] = true
			}
		}
	}

	// 3. Reconcile boot state
	var cmds []agent.Command
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputReconcileBoot{
		Now:                 time.Now().UTC(),
		RunningTmuxSessions: runningSessions,
	})
	if err != nil {
		return fmt.Errorf("reconcile boot failed: %w", err)
	}

	go o.executeCommands(ctx, cmds)
	return nil
}

// SubmitAgent starts provisioning an agent or queues it.
func (o *Orchestrator) SubmitAgent(
	ctx context.Context,
	repoID, harnessName, model, mode, prompt, customBranch, baseBranch string,
) (*agent.Agent, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	r, err := o.store.GetRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("repo %q not found: %w", repoID, err)
	}

	if baseBranch == "" {
		baseBranch = r.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}

	rtName := "docker"
	autoApprove := false
	if r.SettingsJSON != "" {
		var repoSet repo.RepoSettings
		if err := json.Unmarshal([]byte(r.SettingsJSON), &repoSet); err == nil {
			if repoSet.Runtime != "" {
				rtName = repoSet.Runtime
			}
			if repoSet.AutoApprove && rtName == "docker" {
				autoApprove = true
			}
		}
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
		Runtime:      rtName,
		Mode:         mode,
		Prompt:       prompt,
		Branch:       branch,
		BaseBranch:   baseBranch,
		WorktreePath: worktreePath,
		TmuxSession:  tmuxSession,
		AutoApprove:  autoApprove,
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

// RequestApproval submits a structured tool approval request and blocks until a user decision arrives.
func (o *Orchestrator) RequestApproval(ctx context.Context, req agent.ApprovalRequest) (string, error) {
	respCh := make(chan string, 1)

	o.mu.Lock()
	if o.pendingResolutions == nil {
		o.pendingResolutions = make(map[string]chan string)
	}
	o.pendingResolutions[req.ID] = respCh

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputApprovalRequest{
		Now:     time.Now().UTC(),
		Request: req,
	})
	if err != nil {
		delete(o.pendingResolutions, req.ID)
		o.mu.Unlock()
		return "deny", err
	}
	o.mu.Unlock()

	go o.executeCommands(ctx, cmds)

	select {
	case action := <-respCh:
		return action, nil
	case <-ctx.Done():
		o.mu.Lock()
		delete(o.pendingResolutions, req.ID)
		o.mu.Unlock()
		return "deny", ctx.Err()
	}
}

func (o *Orchestrator) resolveApproval(approvalID, action string) {
	o.mu.Lock()
	ch, ok := o.pendingResolutions[approvalID]
	if ok {
		delete(o.pendingResolutions, approvalID)
	}
	o.mu.Unlock()

	if ok && ch != nil {
		select {
		case ch <- action:
		default:
		}
	}
}

// DecideApproval is an alias for ResolveApprovalFromUser.
func (o *Orchestrator) DecideApproval(ctx context.Context, agentID, approvalID string, approved, always bool) error {
	return o.ResolveApprovalFromUser(ctx, agentID, approvalID, approved, always)
}

// ResolveApprovalFromUser is invoked when user clicks Approve, Deny or Always Allow on the phone UI.
func (o *Orchestrator) ResolveApprovalFromUser(ctx context.Context, agentID, approvalID string, approved, always bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputApprovalDecision{
		Now:        time.Now().UTC(),
		AgentID:    agentID,
		ApprovalID: approvalID,
		Approved:   approved,
		Always:     always,
	})
	if err != nil {
		return err
	}

	go o.executeCommands(ctx, cmds)
	return nil
}

// StopAgent terminates an agent process, aborts approvals, and frees concurrency slots.
func (o *Orchestrator) StopAgent(ctx context.Context, agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if cancel, ok := o.cancelFuncs[agentID]; ok {
		cancel()
		delete(o.cancelFuncs, agentID)
	}

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputStopRequested{
		Now:     time.Now().UTC(),
		AgentID: agentID,
	})
	if err != nil {
		return err
	}

	go o.executeCommands(ctx, cmds)
	return nil
}

// CancelAgent cancels a queued task or stops an active agent.
func (o *Orchestrator) CancelAgent(ctx context.Context, agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if cancel, ok := o.cancelFuncs[agentID]; ok {
		cancel()
		delete(o.cancelFuncs, agentID)
	}

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputCancelQueued{
		Now:     time.Now().UTC(),
		AgentID: agentID,
	})
	if err != nil {
		o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputStopRequested{
			Now:     time.Now().UTC(),
			AgentID: agentID,
		})
	}
	if err != nil {
		return err
	}

	go o.executeCommands(ctx, cmds)
	return nil
}

// DeleteAgent deletes an agent and removes its worktree.
func (o *Orchestrator) DeleteAgent(ctx context.Context, agentID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if cancel, ok := o.cancelFuncs[agentID]; ok {
		cancel()
		delete(o.cancelFuncs, agentID)
	}

	var cmds []agent.Command
	var err error
	o.mgrState, cmds, err = agent.Step(o.mgrState, agent.InputDeleteRequested{
		Now:     time.Now().UTC(),
		AgentID: agentID,
	})
	if err != nil {
		return err
	}

	go o.executeCommands(ctx, cmds)
	return nil
}

// GetAgent retrieves an agent from the pure state manager.
func (o *Orchestrator) GetAgent(agentID string) (*agent.Agent, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	a, ok := o.mgrState.Agents[agentID]
	if !ok {
		return nil, false
	}
	return &a, true
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

// Stats returns runtime CPU and RAM stats for an agent.
func (o *Orchestrator) Stats(ctx context.Context, agentID string) (runtime.Usage, error) {
	o.mu.Lock()
	a, ok := o.mgrState.Agents[agentID]
	o.mu.Unlock()
	if !ok {
		return runtime.Usage{}, fmt.Errorf("agent %q not found", agentID)
	}
	rt := o.getRuntime(a.Runtime)
	if rt == nil {
		return runtime.Usage{}, fmt.Errorf("no runtime for %q", a.Runtime)
	}
	return rt.Stats(ctx, runtime.Handle(a.TmuxSession))
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
				FailureReason:    c.Agent.FailureReason,
				AutoApprove:      c.Agent.AutoApprove,
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
			o.mu.Lock()
			a := o.mgrState.Agents[c.AgentID]
			o.mu.Unlock()
			rt := o.getRuntime(a.Runtime)
			if rt != nil {
				_, _ = rt.Exec(ctx, runtime.Handle(c.TmuxSession), []string{string(c.Input)})
			}

		case agent.CmdKillSession:
			o.mu.Lock()
			a := o.mgrState.Agents[c.AgentID]
			o.mu.Unlock()
			rt := o.getRuntime(a.Runtime)
			if rt != nil {
				_ = rt.Destroy(ctx, runtime.Handle(c.TmuxSession))
			}

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

		case agent.CmdResolveApproval:
			o.resolveApproval(c.ApprovalID, c.Action)

		case agent.CmdPersistApproval:
			if c.Decision == "pending" {
				_ = o.store.CreateApproval(ctx, c.ID, c.AgentID, c.Request)
			} else {
				_ = o.store.UpdateApprovalDecision(ctx, c.ID, c.Decision, c.DecidedAt)
			}

		case agent.CmdAuditLog:
			_ = o.store.WriteAudit(ctx, c.Actor, c.Action, c.Target, c.Result)

		case agent.CmdNotifyUser:
			o.logger.Info("push notification", "title", c.Title, "body", c.Body, "url", c.TargetURL)
			o.mu.Lock()
			notif := o.notifier
			o.mu.Unlock()
			if notif != nil {
				notif.NotifyAll(ctx, notifier.NotificationPayload{
					Title:     c.Title,
					Body:      c.Body,
					TargetURL: c.TargetURL,
				})
			}
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
		Runtime:      c.Runtime,
		Prompt:       c.Prompt,
		WorktreePath: c.WorktreePath,
		TmuxSession:  c.TmuxSession,
		AutoApprove:  c.AutoApprove,
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

	rt := o.getRuntime(c.Runtime)
	if rt == nil {
		o.failAgent(c.AgentID, fmt.Sprintf("no runtime available for %q", c.Runtime))
		return
	}

	rtSpec := runtime.RuntimeSpec{
		SessionName:   c.TmuxSession,
		WorkDir:       c.WorktreePath,
		WorktreeMount: c.WorktreePath,
		Command:       cmdArgs,
		Env:           env,
		AutoApprove:   c.AutoApprove,
		MemoryBytes:   700 * 1024 * 1024,
		CPUs:          1.0,
	}

	_, err := rt.Create(ctx, rtSpec)
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
		go o.consumeTaskOutput(agentCtx, c.AgentID, c.TmuxSession, c.Runtime, harn)
	}

	// Monitor session status and OOM kill
	go o.monitorSession(agentCtx, c.AgentID, c.TmuxSession, c.Runtime)
}

func (o *Orchestrator) monitorSession(ctx context.Context, agentID, tmuxSession, runtimeName string) {
	rt := o.getRuntime(runtimeName)
	if rt == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := rt.Inspect(ctx, runtime.Handle(tmuxSession))
			if err != nil {
				continue
			}
			if info.Status == "stopped" || info.Status == "exited" || info.OOMKilled {
				o.mu.Lock()
				a, ok := o.mgrState.Agents[agentID]
				if !ok || agent.IsTerminal(a.State) {
					o.mu.Unlock()
					return
				}
				var nextCmds []agent.Command
				o.mgrState, nextCmds, _ = agent.Step(o.mgrState, agent.InputProcessExited{
					Now:       time.Now().UTC(),
					AgentID:   agentID,
					ExitCode:  info.ExitCode,
					OOMKilled: info.OOMKilled,
				})
				o.mu.Unlock()
				if len(nextCmds) > 0 {
					go o.executeCommands(context.Background(), nextCmds)
				}
				return
			}
		}
	}
}

func (o *Orchestrator) consumeTaskOutput(ctx context.Context, agentID, tmuxSession, runtimeName string, harn harness.Harness) {
	rt := o.getRuntime(runtimeName)
	if rt == nil {
		return
	}
	reader, err := rt.Exec(ctx, runtime.Handle(tmuxSession), nil)
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
