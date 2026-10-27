package orchestrator

import (
	"context"
	"time"

	"agent-dashboard/internal/core/scheduler"
	"agent-dashboard/internal/harness"
)

func (o *Orchestrator) HarnessRegistry() *harness.Registry {
	return o.harnessRegistry
}

func (o *Orchestrator) ReloadSchedules(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	schedules, err := o.store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	var items []scheduler.ScheduleItem
	for _, sc := range schedules {
		items = append(items, scheduler.ScheduleItem{
			ID:         sc.ID,
			TemplateID: sc.TemplateID,
			RepoID:     sc.RepoID,
			Cron:       sc.Cron,
			Enabled:    sc.Enabled,
			LastRunAt:  sc.LastRunAt,
			NextRunAt:  sc.NextRunAt,
		})
	}

	var cmds []scheduler.Command
	o.schedulerState, cmds, err = scheduler.Step(o.schedulerState, scheduler.InputSetSchedules{Schedules: items})
	if err != nil {
		return err
	}
	go o.executeSchedulerCommands(ctx, cmds)
	return nil
}

func (o *Orchestrator) TriggerSchedule(ctx context.Context, scheduleID string) error {
	o.mu.Lock()
	st, cmds, err := scheduler.Step(o.schedulerState, scheduler.InputTriggerManual{
		ScheduleID: scheduleID,
		Now:        time.Now().UTC(),
	})
	if err != nil {
		o.mu.Unlock()
		return err
	}
	o.schedulerState = st
	o.mu.Unlock()

	go o.executeSchedulerCommands(ctx, cmds)
	return nil
}

func (o *Orchestrator) TickScheduler(ctx context.Context, now time.Time) {
	o.mu.Lock()
	st, cmds, err := scheduler.Step(o.schedulerState, scheduler.InputTick{Now: now})
	if err != nil {
		o.mu.Unlock()
		o.logger.Error("scheduler tick error", "error", err)
		return
	}
	o.schedulerState = st
	o.mu.Unlock()

	if len(cmds) > 0 {
		go o.executeSchedulerCommands(ctx, cmds)
	}
}

func (o *Orchestrator) executeSchedulerCommands(ctx context.Context, cmds []scheduler.Command) {
	for _, c := range cmds {
		switch cmd := c.(type) {
		case scheduler.CmdStartScheduledAgent:
			tmpl, err := o.store.GetTemplate(ctx, cmd.TemplateID)
			if err != nil {
				o.logger.Error("failed to load template for schedule", "schedule", cmd.ScheduleID, "template", cmd.TemplateID, "error", err)
				continue
			}
			created, err := o.SubmitAgent(ctx, cmd.RepoID, tmpl.Harness, "", "task", tmpl.Prompt, "", "")
			if err != nil {
				o.logger.Error("failed to start scheduled agent", "schedule", cmd.ScheduleID, "error", err)
				continue
			}
			o.mu.Lock()
			if o.scheduledRuns == nil {
				o.scheduledRuns = make(map[string]string)
			}
			o.scheduledRuns[created.ID] = cmd.ScheduleID
			o.schedulerState, _, _ = scheduler.Step(o.schedulerState, scheduler.InputAgentStarted{
				ScheduleID: cmd.ScheduleID,
				AgentID:    created.ID,
			})
			o.mu.Unlock()
			o.logger.Info("Started scheduled agent", "schedule", cmd.ScheduleID, "agent", created.ID, "template", tmpl.Name)

		case scheduler.CmdUpdateScheduleTimes:
			_ = o.store.UpdateScheduleNextRun(ctx, cmd.ScheduleID, cmd.LastRunAt, cmd.NextRunAt)
		}
	}
}

func (o *Orchestrator) StartSchedulerLoop(ctx context.Context, interval time.Duration) {
	_ = o.ReloadSchedules(ctx)
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				o.TickScheduler(ctx, t)
			}
		}
	}()
}
