package orchestrator

import (
	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/harness"
	"context"
	"encoding/json"
	"time"
)

func (o *Orchestrator) HarnessRegistry() *harness.Registry { return o.harnessRegistry }
func (o *Orchestrator) ReloadSchedules(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	records, err := o.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	items := make([]scheduler.ScheduleItem, 0, len(records))
	for _, r := range records {
		items = append(items, scheduler.ScheduleItem{ID: r.ID, TemplateID: r.TemplateID, RepoID: r.RepoID, Cron: r.Cron, Enabled: r.Enabled, LastRunAt: r.LastRunAt, NextRunAt: r.NextRunAt})
	}
	st, _, err := scheduler.Step(o.schedulerState, scheduler.InputSetSchedules{Schedules: items})
	if err != nil {
		return err
	}
	return o.commitLocked(ctx, o.mgrState, st, nil)
}
func (o *Orchestrator) TriggerSchedule(ctx context.Context, id string) error {
	return o.schedulerTransition(ctx, scheduler.InputTriggerManual{ScheduleID: id, Now: time.Now().UTC()})
}
func (o *Orchestrator) TickScheduler(ctx context.Context, now time.Time) {
	if err := o.schedulerTransition(ctx, scheduler.InputTick{Now: now}); err != nil {
		o.logger.Error("scheduler transition", "error", err)
	}
}

// A schedule reservation, queued agent and runnable spec commit together.
func (o *Orchestrator) schedulerTransition(ctx context.Context, in scheduler.Input) error {
	o.ensureWorker(context.Background())
	o.mu.Lock()
	defer o.mu.Unlock()
	sc, commands, err := scheduler.Step(o.schedulerState, in)
	if err != nil {
		return err
	}
	m := o.mgrState
	var effects []agent.Command
	for _, cmd := range commands {
		start, ok := cmd.(scheduler.CmdStartScheduledAgent)
		if !ok {
			continue
		}
		tmpl, e := o.store.GetTemplate(ctx, start.TemplateID)
		if e != nil {
			return e
		}
		var opts struct {
			Model      string `json:"model"`
			Runtime    string `json:"runtime"`
			Mode       string `json:"mode"`
			BaseBranch string `json:"base_branch"`
		}
		if tmpl.SettingsJSON != "" {
			if e = json.Unmarshal([]byte(tmpl.SettingsJSON), &opts); e != nil {
				return e
			}
		}
		if opts.Mode == "" {
			opts.Mode = "task"
		}
		spec, e := o.prepareSpec(ctx, start.RepoID, tmpl.Harness, opts.Model, opts.Mode, tmpl.Prompt, "", opts.BaseBranch, opts.Runtime)
		if e != nil {
			return e
		}
		var cs []agent.Command
		m, cs, e = agent.Step(m, agent.InputSubmitAgent{Now: start.TriggeredAt, Spec: spec})
		if e != nil {
			return e
		}
		effects = append(effects, cs...)
		sc, _, e = scheduler.Step(sc, scheduler.InputAgentStarted{ScheduleID: start.ScheduleID, AgentID: spec.ID})
		if e != nil {
			return e
		}
	}
	return o.commitLocked(ctx, m, sc, effects)
}
func (o *Orchestrator) StartSchedulerLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if err := o.ReloadSchedules(ctx); err != nil {
		o.logger.Error("reload schedules", "error", err)
		return
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				o.TickScheduler(ctx, now)
			}
		}
	}()
}
