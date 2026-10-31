package scheduler

import (
	"testing"
	"time"
)

func TestScheduleReservationAndFencedCompletion(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	due := now.Add(-time.Minute)
	s := NewState()
	s.Schedules["s"] = ScheduleItem{ID: "s", TemplateID: "t", RepoID: "r", Cron: "* * * * *", Enabled: true, NextRunAt: &due}
	var cmds []Command
	var err error
	s, cmds, err = Step(s, InputTick{Now: now})
	if err != nil || len(cmds) != 2 || s.ActiveRuns["s"] != "pending" {
		t.Fatal("tick did not reserve run", err)
	}
	if _, cmds, err = Step(s, InputTriggerManual{ScheduleID: "s", Now: now}); err == nil || len(cmds) != 0 {
		t.Fatal("manual trigger overlapped pending start")
	}
	s, _, _ = Step(s, InputAgentStarted{ScheduleID: "s", AgentID: "first"})
	s, _, _ = Step(s, InputAgentFinished{ScheduleID: "s", AgentID: "old"})
	if s.ActiveRuns["s"] != "first" {
		t.Fatal("late completion cleared another run")
	}
	s, _, _ = Step(s, InputAgentFinished{ScheduleID: "s", AgentID: "first"})
	s, cmds, err = Step(s, InputTick{Now: now.Add(2 * time.Minute)})
	if err != nil || len(cmds) != 2 {
		t.Fatal("completed schedule could not run again", err)
	}
}
