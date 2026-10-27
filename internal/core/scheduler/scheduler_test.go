package scheduler_test

import (
	"testing"
	"time"

	"agent-dashboard/internal/core/scheduler"
)

func TestCronParser(t *testing.T) {
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

	// @hourly -> 11:00:00
	nextHourly, err := scheduler.NextCronTime("@hourly", base)
	if err != nil {
		t.Fatalf("failed to parse @hourly: %v", err)
	}
	expectedHourly := time.Date(2026, 10, 27, 11, 0, 0, 0, time.UTC)
	if !nextHourly.Equal(expectedHourly) {
		t.Errorf("expected @hourly %v, got %v", expectedHourly, nextHourly)
	}

	// @daily -> 2026-10-28 00:00:00
	nextDaily, err := scheduler.NextCronTime("@daily", base)
	if err != nil {
		t.Fatalf("failed to parse @daily: %v", err)
	}
	expectedDaily := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	if !nextDaily.Equal(expectedDaily) {
		t.Errorf("expected @daily %v, got %v", expectedDaily, nextDaily)
	}

	// 0 2 * * * -> next 02:00:00
	next2am, err := scheduler.NextCronTime("0 2 * * *", base)
	if err != nil {
		t.Fatalf("failed to parse 0 2 * * *: %v", err)
	}
	expected2am := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	if !next2am.Equal(expected2am) {
		t.Errorf("expected 02:00 %v, got %v", expected2am, next2am)
	}

	// Invalid expressions should fail cleanly
	if _, err := scheduler.NextCronTime("invalid cron", base); err == nil {
		t.Errorf("expected error on invalid cron, got nil")
	}
	if _, err := scheduler.NextCronTime("99 * * * *", base); err == nil {
		t.Errorf("expected error on out-of-bounds minute, got nil")
	}
}

func TestSchedulerStateMachine(t *testing.T) {
	st := scheduler.NewState()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	nextRun := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	// 1. Set schedules
	st, cmds, err := scheduler.Step(st, scheduler.InputSetSchedules{
		Schedules: []scheduler.ScheduleItem{
			{
				ID:         "sch-1",
				TemplateID: "tmpl-nightly",
				RepoID:     "repo-core",
				Cron:       "0 2 * * *",
				Enabled:    true,
				NextRunAt:  &nextRun,
			},
			{
				ID:         "sch-disabled",
				TemplateID: "tmpl-test",
				RepoID:     "repo-core",
				Cron:       "@daily",
				Enabled:    false,
				NextRunAt:  &nextRun,
			},
		},
	})
	if err != nil || len(cmds) != 0 {
		t.Fatalf("unexpected error or cmds on SetSchedules: %v, %v", err, cmds)
	}

	// 2. Tick at scheduled time: sch-1 should fire, sch-disabled should not
	st, cmds, err = scheduler.Step(st, scheduler.InputTick{Now: now})
	if err != nil {
		t.Fatalf("tick error: %v", err)
	}
	// Expecting CmdStartScheduledAgent and CmdUpdateScheduleTimes
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands on tick, got %d (%v)", len(cmds), cmds)
	}
	startCmd, ok := cmds[0].(scheduler.CmdStartScheduledAgent)
	if !ok || startCmd.ScheduleID != "sch-1" {
		t.Errorf("expected CmdStartScheduledAgent for sch-1, got %+v", cmds[0])
	}

	// 3. Mark agent started: sch-1 is now active
	st, _, err = scheduler.Step(st, scheduler.InputAgentStarted{
		ScheduleID: "sch-1",
		AgentID:    "agent-run-101",
	})
	if err != nil {
		t.Fatalf("agent started error: %v", err)
	}
	if st.ActiveRuns["sch-1"] != "agent-run-101" {
		t.Errorf("expected active run agent-run-101, got %s", st.ActiveRuns["sch-1"])
	}

	// 4. Tick again while agent is still running: must NOT fire duplicate run
	// Simulate time reaching next run
	st.Schedules["sch-1"] = scheduler.ScheduleItem{
		ID:         "sch-1",
		TemplateID: "tmpl-nightly",
		RepoID:     "repo-core",
		Cron:       "0 2 * * *",
		Enabled:    true,
		NextRunAt:  &now,
	}
	st, cmds, err = scheduler.Step(st, scheduler.InputTick{Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("tick while running error: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands while schedule is actively running, got %d", len(cmds))
	}

	// 5. Agent finished: cleans up active run
	st, _, err = scheduler.Step(st, scheduler.InputAgentFinished{
		ScheduleID: "sch-1",
		AgentID:    "agent-run-101",
		FinishedAt: now.Add(5 * time.Minute),
		Success:    true,
	})
	if err != nil {
		t.Fatalf("agent finished error: %v", err)
	}
	if _, running := st.ActiveRuns["sch-1"]; running {
		t.Errorf("expected sch-1 to no longer be in active runs")
	}

	// 6. Manual trigger
	st, cmds, err = scheduler.Step(st, scheduler.InputTriggerManual{
		ScheduleID: "sch-1",
		Now:        now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("manual trigger error: %v", err)
	}
	if len(cmds) < 1 {
		t.Fatalf("expected CmdStartScheduledAgent on manual trigger")
	}
}
