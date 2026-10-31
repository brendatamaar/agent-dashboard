package orchestrator

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/events"
	"agent-dashboard/internal/core/notification"
	"agent-dashboard/internal/core/repo"
	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/harness"
	"agent-dashboard/internal/harness/antigravity"
	"agent-dashboard/internal/harness/claude"
	"agent-dashboard/internal/harness/codex"
	"agent-dashboard/internal/harness/gemini"
	"agent-dashboard/internal/harness/muse"
	"agent-dashboard/internal/runtime"
	"agent-dashboard/internal/shell/git"
	logutil "agent-dashboard/internal/shell/logger"
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
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

func (b *EventBroadcaster) Broadcast(agentID string, ev events.NormalizedEvent) {
	ev.AgentID = agentID
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, filter := range b.subscribers {
		if filter == "" || filter == agentID {
			select {
			case ch <- ev:
			default:
				delete(b.subscribers, ch)
				close(ch) // Force reconnect/replay instead of silently losing history.
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
	schedulerState     scheduler.State
	version            int64
	store              *store.Store
	gitSvc             *git.Service
	notifier           *notifier.Notifier
	rt                 runtime.Runtime
	runtimes           map[string]runtime.Runtime
	harnesses          map[string]harness.Harness
	harnessRegistry    *harness.Registry
	scheduledRuns      map[string]string
	broadcaster        *EventBroadcaster
	decryptor          SecretDecryptor
	logger             *slog.Logger
	cancelFuncs        map[string]context.CancelFunc
	pendingResolutions map[string]chan string
	wake               chan struct{}
	workerOnce         sync.Once
	workerDone         chan struct{}
	workerCancel       context.CancelFunc
	observerWG         sync.WaitGroup
	shuttingDown       bool
	runCtx             context.Context
	hookURL            string
	outputDone         map[string]chan struct{}
}

func NewOrchestrator(s *store.Store, g *git.Service, rt runtime.Runtime, d SecretDecryptor, log *slog.Logger, cap int) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	if _, ok := log.Handler().(*logutil.ScrubbingHandler); !ok {
		log = slog.New(logutil.NewScrubbingHandler(log.Handler()))
	}
	reg := harness.NewRegistry()
	hs := map[string]harness.Harness{}
	for _, h := range []harness.Harness{claude.NewAdapter(), codex.NewAdapter(), gemini.NewAdapter(), muse.NewAdapter(), antigravity.NewAdapter()} {
		reg.Register(h)
		hs[h.Name()] = h
	}
	rts := map[string]runtime.Runtime{}
	if rt != nil {
		rts[rt.Name()] = rt
	}
	return &Orchestrator{mgrState: agent.NewManagerState(cap), schedulerState: scheduler.NewState(), store: s, gitSvc: g, rt: rt, runtimes: rts, harnesses: hs, harnessRegistry: reg, scheduledRuns: map[string]string{}, broadcaster: NewEventBroadcaster(), decryptor: d, logger: log, cancelFuncs: map[string]context.CancelFunc{}, pendingResolutions: map[string]chan string{}, wake: make(chan struct{}, 1), workerDone: make(chan struct{}), outputDone: map[string]chan struct{}{}}
}
func (o *Orchestrator) RegisterRuntime(name string, rt runtime.Runtime) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runtimes[name] = rt
}
func (o *Orchestrator) RuntimeByName(name string) (runtime.Runtime, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt, ok := o.runtimes[name]
	return rt, ok
}
func (o *Orchestrator) getRuntime(name string) runtime.Runtime {
	rt, _ := o.RuntimeByName(name)
	return rt
}
func (o *Orchestrator) Runtime(name ...string) runtime.Runtime {
	n := "docker"
	if len(name) > 0 {
		n = name[0]
	}
	return o.getRuntime(n)
}
func (o *Orchestrator) RuntimeForAgent(id string) (runtime.Runtime, error) {
	a, ok := o.GetAgent(id)
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	rt, ok := o.RuntimeByName(a.Runtime)
	if !ok {
		return nil, fmt.Errorf("runtime %q unavailable", a.Runtime)
	}
	return rt, nil
}
func (o *Orchestrator) SetNotifier(n *notifier.Notifier) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.notifier = n
}
func (o *Orchestrator) Notifier() *notifier.Notifier {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.notifier
}
func (o *Orchestrator) GitService() *git.Service       { return o.gitSvc }
func (o *Orchestrator) Broadcaster() *EventBroadcaster { return o.broadcaster }
func (o *Orchestrator) SetHookURL(url string)          { o.mu.Lock(); defer o.mu.Unlock(); o.hookURL = url }
func (o *Orchestrator) AuthenticateHook(id, token string) bool {
	if token == "" {
		return false
	}
	a, ok := o.GetAgent(id)
	if !ok || !agent.IsActive(a.State) {
		return false
	}
	hash, err := o.store.HookTokenHash(context.Background(), id)
	return err == nil && subtle.ConstantTimeCompare([]byte(hash), []byte(store.HashToken(token))) == 1
}
func newID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
func (o *Orchestrator) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}
func (o *Orchestrator) ensureWorker(parent context.Context) {
	o.workerOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		o.mu.Lock()
		o.workerCancel = cancel
		o.mu.Unlock()
		go func() { defer close(o.workerDone); o.commandLoop(ctx) }()
	})
}

// Shutdown stops dashboard observers and delivery, preserving tmux/container
// sessions so another dashboard process can recover them.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	o.shuttingDown = true
	cancel := o.workerCancel
	for id, stop := range o.cancelFuncs {
		stop()
		delete(o.cancelFuncs, id)
	}
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-o.workerDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	go func() { o.observerWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// commitLocked is the only writer: no in-memory publication or external effect
// is allowed before the complete state and its commands are durable.
func (o *Orchestrator) commitLocked(ctx context.Context, m agent.ManagerState, sc scheduler.State, cmds []agent.Command) error {
	for scheduleID, id := range sc.ActiveRuns {
		a, ok := m.Agents[id]
		if !ok || agent.IsTerminal(a.State) {
			var err error
			sc, _, err = scheduler.Step(sc, scheduler.InputAgentFinished{ScheduleID: scheduleID, AgentID: id, FinishedAt: time.Now().UTC()})
			if err != nil {
				return err
			}
		}
	}
	cleaned := make([]agent.Command, 0, len(cmds))
	for _, cmd := range cmds {
		switch c := cmd.(type) {
		case agent.CmdPersistEvent:
			c.Event = o.scrubEvent(c.Event)
			cleaned = append(cleaned, c)
		case agent.CmdPersistApproval:
			c.Request = o.scrubJSON(c.Request)
			cleaned = append(cleaned, c)
		case agent.CmdAuditLog:
			c.Actor = logutil.Scrub(o.logger, c.Actor)
			c.Action = logutil.Scrub(o.logger, c.Action)
			c.Target = logutil.Scrub(o.logger, c.Target)
			c.Result = logutil.Scrub(o.logger, c.Result)
			cleaned = append(cleaned, c)
		case agent.CmdNotifyUser:
			c.Body = logutil.Scrub(o.logger, c.Body)
			cleaned = append(cleaned, c)
		default:
			cleaned = append(cleaned, cmd)
		}
	}
	for id, req := range m.PendingApprovals {
		req.Command = logutil.Scrub(o.logger, req.Command)
		req.Diff = logutil.Scrub(o.logger, req.Diff)
		req.FilePath = logutil.Scrub(o.logger, req.FilePath)
		if req.ToolInput != nil {
			req.ToolInput = o.scrubValue(req.ToolInput).(map[string]any)
		}
		m.PendingApprovals[id] = req
	}

	emitted, err := o.store.CommitOrchestration(ctx, o.version, m, sc, cleaned)
	if err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}
	o.version++
	o.mgrState = m
	o.schedulerState = sc
	for _, ev := range emitted {
		o.broadcaster.Broadcast(ev.AgentID, ev)
	}
	o.signal()
	return nil
}
func (o *Orchestrator) transition(ctx context.Context, in agent.Input) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.transitionLocked(ctx, in)
}
func (o *Orchestrator) transitionLocked(ctx context.Context, in agent.Input) error {
	next, cmds, err := agent.Step(o.mgrState, in)
	if err != nil {
		return err
	}
	sc := o.schedulerState
	// Finish only the run registered for this schedule. Old completion cannot
	// clear a newer run, and recovery restores this association from the snapshot.
	for scheduleID, id := range sc.ActiveRuns {
		a, ok := next.Agents[id]
		if ok && agent.IsTerminal(a.State) {
			var e error
			sc, _, e = scheduler.Step(sc, scheduler.InputAgentFinished{ScheduleID: scheduleID, AgentID: id, FinishedAt: time.Now().UTC()})
			if e != nil {
				return e
			}
		}
	}
	return o.commitLocked(ctx, next, sc, cmds)
}
func (o *Orchestrator) scrubValue(v any) any {
	switch x := v.(type) {
	case string:
		return logutil.Scrub(o.logger, x)
	case map[string]any:
		r := make(map[string]any, len(x))
		for k, v := range x {
			r[logutil.Scrub(o.logger, k)] = o.scrubValue(v)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = o.scrubValue(v)
		}
		return r
	default:
		return v
	}
}
func (o *Orchestrator) scrubJSON(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return logutil.Scrub(o.logger, raw)
	}
	b, err := json.Marshal(o.scrubValue(v))
	if err != nil {
		return "{}"
	}
	return string(b)
}
func (o *Orchestrator) scrubEvent(ev events.NormalizedEvent) events.NormalizedEvent {
	b, err := json.Marshal(ev.Payload)
	if err != nil {
		ev.Payload = map[string]any{"message": "invalid event payload"}
		return ev
	}
	var payload map[string]any
	if json.Unmarshal(b, &payload) == nil {
		ev.Payload = o.scrubValue(payload).(map[string]any)
	}
	return ev
}

func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	o.runCtx = ctx
	var importedCommands []agent.Command
	imported := false
	m, sc, v, err := o.store.LoadOrchestration(ctx)
	if errors.Is(err, store.ErrNotFound) {
		imported = true
		records, e := o.store.ListAgents(ctx)
		if e != nil {
			o.mu.Unlock()
			return e
		}
		m = o.mgrState
		sc = o.schedulerState
		// Upgrade legacy rows in deterministic submission order; new snapshots keep
		// the original full spec and queue order, including equal timestamps.
		for i := len(records) - 1; i >= 0; i-- {
			r := records[i]
			rp, e := o.store.GetRepo(ctx, r.RepoID)
			if e != nil {
				o.mu.Unlock()
				return e
			}
			a := agent.Agent{ID: r.ID, RepoID: r.RepoID, RepoPath: rp.Path, Harness: r.Harness, Model: r.Model, Runtime: r.Runtime, Mode: r.Mode, Branch: r.Branch, BaseBranch: rp.DefaultBranch, WorktreePath: r.WorktreePath, TmuxSession: r.TmuxSession, HarnessSessionID: r.HarnessSessionID, State: agent.State(r.State), FailureReason: r.FailureReason, AutoApprove: r.AutoApprove, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
			settings := repo.DefaultRepoSettings()
			if rp.SettingsJSON != "" {
				if e = json.Unmarshal([]byte(rp.SettingsJSON), &settings); e != nil {
					o.mu.Unlock()
					return e
				}
			}
			a.Image = settings.Image
			a.SetupCommand = settings.SetupCommand
			a.EnvVars = settings.EnvVars
			a.AllowedTools = settings.AllowedTools
			if a.State == agent.StateQueued || a.State == agent.StateProvisioning {
				a.State = agent.StateFailed
				a.FailureReason = "legacy_missing_runnable_spec"
				a.UpdatedAt = time.Now().UTC()
				message := "The previous dashboard version did not save this agent's prompt. Resubmit this task; its existing worktree has been preserved."
				importedCommands = append(importedCommands, agent.CmdPersistEvent{AgentID: a.ID, Event: events.NewErrorEvent(0, a.UpdatedAt, message, nil)})
			}
			m.Agents[a.ID] = a
		}
		err = nil
	}
	if err != nil {
		o.mu.Unlock()
		return err
	}
	o.mgrState = m
	o.schedulerState = sc
	o.version = v
	if imported {
		approvals, e := o.store.ListApprovals(ctx, "")
		if e != nil {
			o.mu.Unlock()
			return e
		}
		for _, row := range approvals {
			a, ok := m.Agents[row.AgentID]
			if !ok || !agent.IsActive(a.State) {
				continue
			}
			var req agent.ApprovalRequest
			if e = json.Unmarshal([]byte(row.RequestJSON), &req); e != nil {
				o.mu.Unlock()
				return e
			}
			switch row.Decision {
			case "pending":
				m.PendingApprovals[req.ID] = req
			case "always_allowed":
				if m.AlwaysAllowedTools[a.ID] == nil {
					m.AlwaysAllowedTools[a.ID] = map[string]bool{}
				}
				m.AlwaysAllowedTools[a.ID][req.ToolName] = true
			}
		}
		if e = o.commitLocked(ctx, m, sc, importedCommands); e != nil {
			o.mu.Unlock()
			return e
		}
	}
	active := make([]agent.Agent, 0)
	for _, a := range m.Agents {
		if agent.IsActive(a.State) {
			active = append(active, a)
		}
	}
	o.mu.Unlock()
	o.ensureWorker(ctx)
	o.signal()
	for _, a := range active {
		rt := o.getRuntime(a.Runtime)
		if rt == nil {
			return fmt.Errorf("recovery runtime %q unavailable", a.Runtime)
		}
		info, e := rt.Inspect(ctx, runtime.Handle(a.TmuxSession))
		if e != nil {
			if errors.Is(e, runtime.ErrNotFound) && a.State != agent.StateProvisioning {
				pending, checkErr := o.store.HasPendingSessionStart(ctx, a.ID, a.TmuxSession)
				if checkErr != nil {
					return checkErr
				}
				if pending {
					continue
				}
				if err = o.failAgent(a.ID, "runtime session disappeared during restart"); err != nil {
					return err
				}
			} else {
				o.logger.Warn("runtime inspection deferred", "agent", a.ID, "error", e)
				o.attachRun(a)
			}
			continue
		}
		if info.Status == "running" || a.State != agent.StateProvisioning {
			o.attachRun(a)
		}
	}
	return nil
}
func (o *Orchestrator) SubmitAgent(ctx context.Context, repoID, harnessName, model, mode, prompt, customBranch, baseBranch string, runtimeOpt ...string) (*agent.Agent, error) {
	o.ensureWorker(context.Background())
	o.mu.Lock()
	defer o.mu.Unlock()
	spec, err := o.prepareSpec(ctx, repoID, harnessName, model, mode, prompt, customBranch, baseBranch, runtimeOpt...)
	if err != nil {
		return nil, err
	}
	next, cmds, err := agent.Step(o.mgrState, agent.InputSubmitAgent{Now: time.Now().UTC(), Spec: spec})
	if err != nil {
		return nil, err
	}
	if err = o.commitLocked(ctx, next, o.schedulerState, cmds); err != nil {
		return nil, err
	}
	a := next.Agents[spec.ID]
	return &a, nil
}
func (o *Orchestrator) prepareSpec(ctx context.Context, repoID, harnessName, model, mode, prompt, customBranch, baseBranch string, runtimeOpt ...string) (agent.AgentSpec, error) {
	r, err := o.store.GetRepo(ctx, repoID)
	if err != nil {
		return agent.AgentSpec{}, err
	}
	settings := repo.DefaultRepoSettings()
	if r.SettingsJSON != "" {
		if err = json.Unmarshal([]byte(r.SettingsJSON), &settings); err != nil {
			return agent.AgentSpec{}, err
		}
	}
	if len(runtimeOpt) > 0 && runtimeOpt[0] != "" {
		settings.Runtime = runtimeOpt[0]
	}
	if err = repo.ValidateRepoSettings(settings); err != nil {
		return agent.AgentSpec{}, err
	}
	if _, ok := o.harnesses[harnessName]; !ok {
		return agent.AgentSpec{}, fmt.Errorf("unknown harness %q", harnessName)
	}
	if mode != "task" && mode != "interactive" {
		return agent.AgentSpec{}, fmt.Errorf("invalid agent mode")
	}
	if baseBranch == "" {
		baseBranch = r.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if err = repo.ValidateBranchName(baseBranch); err != nil {
		return agent.AgentSpec{}, err
	}
	id := newID("agent-")
	slug := repo.SanitizeSlug(prompt)
	if customBranch != "" {
		if err = repo.ValidateBranchName(customBranch); err != nil {
			return agent.AgentSpec{}, err
		}
		slug = repo.SanitizeSlug(customBranch)
	}
	suffix := strings.TrimPrefix(id, "agent-")
	branch := "agent/" + slug + "-" + suffix
	if customBranch != "" {
		branch = customBranch
	}
	path := filepath.Join(o.gitSvc.WorkspaceDir(), r.Name+"-wt-"+slug+"-"+suffix)
	spec := agent.AgentSpec{ID: id, RepoID: r.ID, RepoPath: r.Path, Harness: harnessName, Model: model, Runtime: settings.Runtime, Mode: mode, Prompt: prompt, Branch: branch, BaseBranch: baseBranch, WorktreePath: path, TmuxSession: "ad-" + id, AutoApprove: settings.AutoApprove, Image: settings.Image, SetupCommand: settings.SetupCommand, EnvVars: settings.EnvVars, AllowedTools: settings.AllowedTools}
	if err = harness.ValidateSpec(o.harnesses[harnessName], spec); err != nil {
		return agent.AgentSpec{}, err
	}
	return spec, nil
}
func (o *Orchestrator) SendMessage(ctx context.Context, id, msg string) error {
	return o.transition(ctx, agent.InputSendMessage{Now: time.Now().UTC(), AgentID: id, Message: msg})
}
func (o *Orchestrator) StopAgent(ctx context.Context, id string) error {
	return o.transition(ctx, agent.InputStopRequested{Now: time.Now().UTC(), AgentID: id})
}
func (o *Orchestrator) CancelAgent(ctx context.Context, id string) error { return o.StopAgent(ctx, id) }
func (o *Orchestrator) DeleteAgent(ctx context.Context, id string) error {
	return o.transition(ctx, agent.InputDeleteRequested{Now: time.Now().UTC(), AgentID: id})
}
func (o *Orchestrator) ResumeAgent(ctx context.Context, id string) error {
	return o.transition(ctx, agent.InputResumeRequested{Now: time.Now().UTC(), AgentID: id, TmuxSession: newID("ad-resume-")})
}
func (o *Orchestrator) InterruptAgent(ctx context.Context, id string) error {
	return o.transition(ctx, agent.InputInterruptRequested{Now: time.Now().UTC(), AgentID: id})
}
func (o *Orchestrator) DecideApproval(ctx context.Context, id, approvalID string, approved, always bool) error {
	return o.ResolveApprovalFromUser(ctx, id, approvalID, approved, always)
}
func (o *Orchestrator) ResolveApprovalFromUser(ctx context.Context, id, approvalID string, approved, always bool) error {
	return o.transition(ctx, agent.InputApprovalDecision{Now: time.Now().UTC(), AgentID: id, ApprovalID: approvalID, Approved: approved, Always: always})
}
func (o *Orchestrator) RequestApproval(ctx context.Context, req agent.ApprovalRequest) (string, error) {
	o.mu.Lock()
	a, active := o.mgrState.Agents[req.AgentID]
	if !active || !agent.IsActive(a.State) {
		o.mu.Unlock()
		return "deny", fmt.Errorf("agent is not active")
	}
	req.Command = logutil.Scrub(o.logger, req.Command)
	req.Diff = logutil.Scrub(o.logger, req.Diff)
	req.FilePath = logutil.Scrub(o.logger, req.FilePath)
	if req.ToolInput != nil {
		b, _ := json.Marshal(req.ToolInput)
		var input map[string]any
		if json.Unmarshal(b, &input) == nil {
			req.ToolInput = o.scrubValue(input).(map[string]any)
		}
	}

	if existing, err := o.store.GetApproval(ctx, req.ID); err == nil {
		if existing.AgentID != req.AgentID {
			o.mu.Unlock()
			return "deny", fmt.Errorf("approval belongs to another agent")
		}
		var prior agent.ApprovalRequest
		if json.Unmarshal([]byte(existing.RequestJSON), &prior) != nil {
			o.mu.Unlock()
			return "deny", fmt.Errorf("invalid stored approval")
		}
		prior.CreatedAt = time.Time{}
		candidate := req
		candidate.CreatedAt = time.Time{}
		oldJSON, _ := json.Marshal(prior)
		newJSON, _ := json.Marshal(candidate)
		if string(oldJSON) != string(newJSON) {
			o.mu.Unlock()
			return "deny", fmt.Errorf("approval id reused for a different request")
		}

		if existing.Decision != "pending" {
			o.mu.Unlock()
			if existing.Decision == "approved" || existing.Decision == "always_allowed" {
				return "allow", nil
			}
			return "deny", nil
		}
	}
	if _, waiting := o.pendingResolutions[req.ID]; waiting {
		o.mu.Unlock()
		return "deny", fmt.Errorf("approval already waiting")
	}
	ch := make(chan string, 1)
	o.pendingResolutions[req.ID] = ch
	err := o.transitionLocked(ctx, agent.InputApprovalRequest{Now: time.Now().UTC(), Request: req})
	if err != nil {
		delete(o.pendingResolutions, req.ID)
		o.mu.Unlock()
		return "deny", err
	}
	o.mu.Unlock()
	select {
	case action := <-ch:
		return action, nil
	case <-ctx.Done():
		o.mu.Lock()
		if o.pendingResolutions[req.ID] == ch {
			delete(o.pendingResolutions, req.ID)
		}
		o.mu.Unlock()
		return "deny", ctx.Err()
	}
}
func (o *Orchestrator) resolveApproval(id, action string) {
	o.mu.Lock()
	ch := o.pendingResolutions[id]
	delete(o.pendingResolutions, id)
	o.mu.Unlock()
	if ch != nil {
		ch <- action
	}
}
func (o *Orchestrator) GetAgent(id string) (*agent.Agent, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	a, ok := o.mgrState.Agents[id]
	return &a, ok
}
func (o *Orchestrator) ListAgents() []agent.Agent {
	o.mu.Lock()
	defer o.mu.Unlock()
	as := make([]agent.Agent, 0, len(o.mgrState.Agents))
	for _, a := range o.mgrState.Agents {
		as = append(as, a)
	}
	return as
}
func (o *Orchestrator) Stats(ctx context.Context, id string) (runtime.Usage, error) {
	a, ok := o.GetAgent(id)
	if !ok {
		return runtime.Usage{}, fmt.Errorf("agent not found")
	}
	rt, err := o.RuntimeForAgent(id)
	if err != nil {
		return runtime.Usage{}, err
	}
	return rt.Stats(ctx, runtime.Handle(a.TmuxSession))
}

func (o *Orchestrator) commandLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.wake:
		case <-ticker.C:
		}
		for {
			record, err := o.store.NextOutbox(ctx)
			if errors.Is(err, store.ErrNotFound) {
				break
			}
			if err != nil {
				o.logger.Error("read command outbox", "error", err)
				return
			}
			cmd, err := store.DecodeCommand(record.Kind, record.Payload)
			if err != nil {
				o.logger.Error("decode durable command", "error", err)
				break
			}
			if record.Status == "started" && !idempotent(cmd) {
				o.logger.Warn("command outcome unknown; not redelivered", "command", record.Kind, "id", record.ID)
				if id := commandAgentID(cmd); id != "" {
					o.mu.Lock()
					if _, ok := o.mgrState.Agents[id]; ok {
						err = o.commitLocked(ctx, o.mgrState.Clone(), o.schedulerState, []agent.Command{agent.CmdPersistEvent{AgentID: id, Event: events.NewMessageEvent(0, time.Now().UTC(), "system", "Previous input delivery has an unknown outcome after restart. Check the terminal before sending it again.")}})
					}
					o.mu.Unlock()
					if err != nil {
						break
					}
				}
				if err = o.store.CompleteOutbox(ctx, record.ID); err != nil {
					break
				}
				continue
			}
			if err = o.store.StartOutbox(ctx, record.ID); err != nil {
				break
			}
			if err = o.executeCommand(ctx, cmd); err != nil {
				o.logger.Error("execute durable command", "command", record.Kind, "error", err)
				break
			}
			if err = o.store.CompleteOutbox(ctx, record.ID); err != nil {
				break
			}
		}
	}
}
func idempotent(cmd agent.Command) bool {
	switch cmd.(type) {
	case *agent.CmdSendInput, *agent.CmdInterruptSession, *agent.CmdNotifyUser:
		return false
	}
	return true
}
func (o *Orchestrator) executeCommand(ctx context.Context, cmd agent.Command) error {
	switch c := cmd.(type) {
	case *agent.CmdCreateWorktree:
		a, ok := o.GetAgent(c.AgentID)
		if !ok || a.State != agent.StateProvisioning {
			return nil
		}
		var err error
		if !a.Resuming {
			err = o.gitSvc.EnsureWorktree(ctx, c.RepoPath, c.BaseBranch, c.Branch, c.WorktreePath)
		}
		if err != nil {
			return o.transition(ctx, agent.InputWorktreeFailed{Now: time.Now().UTC(), AgentID: c.AgentID, Error: err.Error()})
		}
		return o.transition(ctx, agent.InputWorktreeReady{Now: time.Now().UTC(), AgentID: c.AgentID, WorktreePath: c.WorktreePath})
	case *agent.CmdStartSession:
		return o.startAgentSession(ctx, *c)
	case *agent.CmdSendInput:
		a, ok := o.GetAgent(c.AgentID)
		if !ok || !agent.IsActive(a.State) || a.TmuxSession != c.TmuxSession {
			return nil
		}
		rt := o.getRuntime(a.Runtime)
		if rt == nil {
			return fmt.Errorf("runtime unavailable")
		}
		r, err := rt.Exec(ctx, runtime.Handle(c.TmuxSession), []string{strings.TrimSuffix(string(c.Input), "\n")})
		if r != nil {
			_ = r.Close()
		}
		return err
	case *agent.CmdKillSession:
		rt := o.getRuntime(c.Runtime)
		if rt == nil {
			return fmt.Errorf("runtime unavailable")
		}
		if err := rt.Destroy(ctx, runtime.Handle(c.TmuxSession)); err != nil {
			return err
		}
		o.mu.Lock()
		if cancel := o.cancelFuncs[c.AgentID]; cancel != nil {
			cancel()
			delete(o.cancelFuncs, c.AgentID)
		}
		o.mu.Unlock()
		return o.transition(ctx, agent.InputSessionStopped{Now: time.Now().UTC(), AgentID: c.AgentID})
	case *agent.CmdInterruptSession:
		a, ok := o.GetAgent(c.AgentID)
		if !ok || a.TmuxSession != c.TmuxSession || !agent.IsActive(a.State) {
			return nil
		}
		rt := o.getRuntime(c.Runtime)
		if rt == nil {
			return fmt.Errorf("runtime unavailable")
		}
		return rt.Interrupt(ctx, runtime.Handle(c.TmuxSession))
	case *agent.CmdRemoveWorktree:
		return o.gitSvc.RemoveWorktree(ctx, c.RepoPath, c.WorktreePath, c.Branch)
	case *agent.CmdResolveApproval:
		o.resolveApproval(c.ApprovalID, c.Action)
	case *agent.CmdNotifyUser:
		if n := o.Notifier(); n != nil {
			priority := notification.PriorityNormal
			if c.Urgent {
				priority = notification.PriorityUrgent
			}
			n.NotifyAll(ctx, notifier.NotificationPayload{Title: c.Title, Body: c.Body, TargetURL: c.TargetURL, Priority: priority})
		}
	default:
		return fmt.Errorf("unhandled command %T", cmd)
	}
	return nil
}
func (o *Orchestrator) startAgentSession(ctx context.Context, c agent.CmdStartSession) error {
	a, ok := o.GetAgent(c.AgentID)
	if !ok || a.State != agent.StateRunning || a.TmuxSession != c.TmuxSession {
		return nil
	}
	rt := o.getRuntime(c.Runtime)
	if rt == nil {
		return o.failAgent(c.AgentID, "runtime unavailable")
	}
	h, ok := o.harnesses[c.Harness]
	if !ok {
		return o.failAgent(c.AgentID, "harness unavailable")
	}
	sessions, err := rt.ListSessions(ctx)
	if err != nil {
		return err
	}
	for _, id := range sessions {
		if id == c.TmuxSession {
			o.attachRun(*a)
			return nil
		}
	}
	if info, e := rt.Inspect(ctx, runtime.Handle(c.TmuxSession)); e == nil && (info.Status == "stopped" || info.Status == "exited") {
		o.attachRun(*a)
		return nil
	} else if e != nil && !errors.Is(e, runtime.ErrNotFound) {
		return e
	}
	spec := agent.AgentSpec{ID: a.ID, Mode: a.Mode, Model: a.Model, Runtime: a.Runtime, Prompt: a.Prompt, WorktreePath: a.WorktreePath, TmuxSession: a.TmuxSession, AutoApprove: a.AutoApprove, AllowedTools: a.AllowedTools}
	if err := harness.ValidateSpec(h, spec); err != nil {
		return o.failAgent(c.AgentID, err.Error())
	}
	args := h.StartCommand(spec)
	if c.ResumeID != "" {
		if err := repo.ValidateRepoName(c.ResumeID); err != nil {
			return o.failAgent(c.AgentID, "invalid resumable harness session identity")
		}
		args = h.ResumeCommand(spec, c.ResumeID)
	}
	env := make(map[string]string)
	for k, v := range a.EnvVars {
		env[k] = v
	}
	if o.decryptor != nil {
		keys := credentialKeys(a.Harness)
		for _, entry := range keys {
			value, e := o.decryptor.DecryptSecret(ctx, entry[0])
			if e == nil && value != "" {
				env[entry[1]] = value
				logutil.RegisterSecrets(o.logger, value)
			}
		}
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	if err = o.store.SaveHookToken(ctx, a.ID, store.HashToken(token)); err != nil {
		return err
	}
	logutil.RegisterSecrets(o.logger, token)
	env["AGENT_DASHBOARD_AGENT_ID"] = a.ID
	env["AGENT_DASHBOARD_HOOK_TOKEN"] = token
	o.mu.Lock()
	hookURL := o.hookURL
	o.mu.Unlock()
	if hookURL != "" {
		env["AGENT_DASHBOARD_HOOK_URL"] = hookURL
	}
	rtSpec := runtime.RuntimeSpec{Mode: a.Mode, SessionName: c.TmuxSession, WorkDir: c.WorktreePath, WorktreeMount: c.WorktreePath, Command: args, Env: env, Image: a.Image, SetupCommand: a.SetupCommand, AutoApprove: c.AutoApprove, MemoryBytes: 700 * 1024 * 1024, CPUs: 1}
	if _, err = rt.Create(ctx, rtSpec); err != nil {
		return o.failAgent(c.AgentID, fmt.Sprintf("start session: %v", err))
	}
	// Stop may have committed while Create was in flight. The ordered kill command
	// will destroy this exact handle before releasing the reserved capacity.
	current, exists := o.GetAgent(c.AgentID)
	if exists && agent.IsActive(current.State) && current.TmuxSession == c.TmuxSession {
		o.attachRun(*current)
	}
	return nil
}
func credentialKeys(name string) [][2]string {
	switch name {
	case "claude":
		return [][2]string{{"claude_token", "ANTHROPIC_AUTH_TOKEN"}, {"anthropic_auth_token", "ANTHROPIC_AUTH_TOKEN"}, {"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"}, {"anthropic_api_key", "ANTHROPIC_API_KEY"}, {"ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"}}
	case "codex":
		return [][2]string{{"openai_api_key", "OPENAI_API_KEY"}, {"OPENAI_API_KEY", "OPENAI_API_KEY"}}
	case "gemini":
		return [][2]string{{"gemini_api_key", "GEMINI_API_KEY"}, {"GEMINI_API_KEY", "GEMINI_API_KEY"}}
	case "muse":
		return [][2]string{{"muse_token", "MUSE_API_KEY"}, {"muse_api_key", "MUSE_API_KEY"}, {"MUSE_API_KEY", "MUSE_API_KEY"}}
	case "antigravity":
		return [][2]string{{"agy_token", "AGY_TOKEN"}, {"AGY_TOKEN", "AGY_TOKEN"}, {"ANTIGRAVITY_API_KEY", "ANTIGRAVITY_API_KEY"}}
	}
	return nil
}
func (o *Orchestrator) attachRun(a agent.Agent) {
	o.mu.Lock()
	if o.shuttingDown {
		o.mu.Unlock()
		return
	}
	if old := o.cancelFuncs[a.ID]; old != nil {
		o.mu.Unlock()
		return
	}
	parent := o.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	o.cancelFuncs[a.ID] = cancel
	done := make(chan struct{})
	o.outputDone[a.ID] = done
	o.observerWG.Add(1)
	if a.Mode == "task" {
		o.observerWG.Add(1)
	}
	o.mu.Unlock()
	if a.Mode == "task" {
		go func() {
			defer o.observerWG.Done()
			defer close(done)
			for {
				err := o.consumeTaskOutput(ctx, a, o.harnesses[a.Harness])
				if err == nil || ctx.Err() != nil {
					return
				}
				o.logger.Warn("retry durable output replay", "agent", a.ID, "error", err)
				timer := time.NewTimer(2 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	} else {
		close(done)
	}
	go func() { defer o.observerWG.Done(); o.monitorSession(ctx, a) }()
}
func (o *Orchestrator) monitorSession(ctx context.Context, a agent.Agent) {
	rt := o.getRuntime(a.Runtime)
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
			info, err := rt.Inspect(ctx, runtime.Handle(a.TmuxSession))
			if err != nil {
				continue
			}
			if info.Status != "stopped" && info.Status != "exited" && !info.OOMKilled {
				continue
			}
			o.mu.Lock()
			done := o.outputDone[a.ID]
			o.mu.Unlock()
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return
				}
			}
			o.mu.Lock()
			current, ok := o.mgrState.Agents[a.ID]
			if !ok || current.TmuxSession != a.TmuxSession {
				o.mu.Unlock()
				return
			}
			err = o.transitionLocked(context.Background(), agent.InputProcessExited{Now: time.Now().UTC(), AgentID: a.ID, ExitCode: info.ExitCode, OOMKilled: info.OOMKilled})
			delete(o.cancelFuncs, a.ID)
			o.mu.Unlock()
			if err != nil {
				o.logger.Error("persist process exit", "error", err)
				continue
			}
			return
		}
	}
}
func (o *Orchestrator) consumeTaskOutput(ctx context.Context, a agent.Agent, h harness.Harness) error {
	if h == nil {
		return nil
	}
	rt := o.getRuntime(a.Runtime)
	if rt == nil {
		return nil
	}
	r, err := rt.Output(ctx, runtime.Handle(a.TmuxSession))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	evs, errs := h.ParseEvents(ctx, r)
	var sourceSeq int64
	for evs != nil || errs != nil {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-errs:
			if !ok {
				errs = nil
			} else if err != nil && err != io.EOF {
				o.logger.Warn("structured output unavailable; terminal remains accessible", "agent", a.ID, "error", err)
			}
		case ev, ok := <-evs:
			if !ok {
				evs = nil
				continue
			}
			sourceSeq++
			o.mu.Lock()
			current, exists := o.mgrState.Agents[a.ID]
			if !exists || current.TmuxSession != a.TmuxSession {
				o.mu.Unlock()
				return nil
			}
			if sourceSeq <= current.OutputCursor {
				o.mu.Unlock()
				continue
			}
			next := o.mgrState.Clone()
			current.OutputCursor = sourceSeq
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" && repo.ValidateRepoName(sid) == nil {
				current.HarnessSessionID = sid
			}
			next.Agents[a.ID] = current
			st, cmds, e := agent.Step(next, agent.InputHarnessEvent{Now: time.Now().UTC(), AgentID: a.ID, Event: o.scrubEvent(ev)})
			if e == nil {
				e = o.commitLocked(context.Background(), st, o.schedulerState, cmds)
			}
			o.mu.Unlock()
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func (o *Orchestrator) failAgent(id, message string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	st, cmds, err := agent.Step(o.mgrState, agent.InputHarnessEvent{Now: time.Now().UTC(), AgentID: id, Event: events.NewErrorEvent(0, time.Now().UTC(), message, nil)})
	if err != nil {
		return err
	}
	return o.commitLocked(context.Background(), st, o.schedulerState, cmds)
}

func commandAgentID(cmd agent.Command) string {
	switch c := cmd.(type) {
	case *agent.CmdSendInput:
		return c.AgentID
	case *agent.CmdInterruptSession:
		return c.AgentID
	}
	return ""
}

// RecordHarnessSession captures IDs reported by interactive hooks so an explicit
// resume can reuse the provider's conversation instead of starting a new one.
func (o *Orchestrator) RecordHarnessSession(ctx context.Context, id, sessionID string) error {
	if err := repo.ValidateRepoName(sessionID); err != nil {
		return fmt.Errorf("invalid harness session id: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	a, ok := o.mgrState.Agents[id]
	if !ok || !agent.IsActive(a.State) {
		return fmt.Errorf("agent is not active")
	}
	if a.HarnessSessionID == sessionID {
		return nil
	}
	return o.transitionLocked(ctx, agent.InputSessionStarted{Now: time.Now().UTC(), AgentID: id, TmuxSession: a.TmuxSession, HarnessSessionID: sessionID})
}
