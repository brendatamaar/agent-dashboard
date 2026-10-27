package scheduler

import (
	"errors"
	"fmt"
	"time"
)

type ScheduleItem struct {
	ID         string     `json:"id"`
	TemplateID string     `json:"template_id"`
	RepoID     string     `json:"repo_id"`
	Cron       string     `json:"cron"`
	Enabled    bool       `json:"enabled"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
}

type State struct {
	Schedules  map[string]ScheduleItem `json:"schedules"`
	ActiveRuns map[string]string       `json:"active_runs"` // scheduleID -> agentID
}

func NewState() State {
	return State{
		Schedules:  make(map[string]ScheduleItem),
		ActiveRuns: make(map[string]string),
	}
}

// Inputs
type Input interface {
	isSchedulerInput()
}

type InputSetSchedules struct {
	Schedules []ScheduleItem
}

func (InputSetSchedules) isSchedulerInput() {}

type InputTick struct {
	Now time.Time
}

func (InputTick) isSchedulerInput() {}

type InputTriggerManual struct {
	ScheduleID string
	Now        time.Time
}

func (InputTriggerManual) isSchedulerInput() {}

type InputEnableSchedule struct {
	ScheduleID string
	NextRun    time.Time
}

func (InputEnableSchedule) isSchedulerInput() {}

type InputDisableSchedule struct {
	ScheduleID string
}

func (InputDisableSchedule) isSchedulerInput() {}

type InputAgentStarted struct {
	ScheduleID string
	AgentID    string
}

func (InputAgentStarted) isSchedulerInput() {}

type InputAgentFinished struct {
	ScheduleID string
	AgentID    string
	FinishedAt time.Time
	Success    bool
	NextRun    *time.Time
}

func (InputAgentFinished) isSchedulerInput() {}

// Commands
type Command interface {
	isSchedulerCommand()
}

type CmdStartScheduledAgent struct {
	ScheduleID  string    `json:"schedule_id"`
	TemplateID  string    `json:"template_id"`
	RepoID      string    `json:"repo_id"`
	TriggeredAt time.Time `json:"triggered_at"`
}

func (CmdStartScheduledAgent) isSchedulerCommand() {}

type CmdUpdateScheduleTimes struct {
	ScheduleID string     `json:"schedule_id"`
	LastRunAt  *time.Time `json:"last_run_at"`
	NextRunAt  *time.Time `json:"next_run_at"`
}

func (CmdUpdateScheduleTimes) isSchedulerCommand() {}

var (
	ErrScheduleNotFound = errors.New("schedule not found")
)

// Step transitions the scheduler state machine purely: (state₂, commands) = Step(state₁, input)
func Step(st State, in Input) (State, []Command, error) {
	// Copy state for purity
	nextSt := State{
		Schedules:  make(map[string]ScheduleItem, len(st.Schedules)),
		ActiveRuns: make(map[string]string, len(st.ActiveRuns)),
	}
	for k, v := range st.Schedules {
		nextSt.Schedules[k] = v
	}
	for k, v := range st.ActiveRuns {
		nextSt.ActiveRuns[k] = v
	}

	var cmds []Command

	switch input := in.(type) {
	case InputSetSchedules:
		nextSt.Schedules = make(map[string]ScheduleItem, len(input.Schedules))
		for _, sc := range input.Schedules {
			nextSt.Schedules[sc.ID] = sc
		}

	case InputTick:
		now := input.Now
		for id, sc := range nextSt.Schedules {
			if !sc.Enabled {
				continue
			}
			// Check if eligible to run
			if sc.NextRunAt != nil && !now.Before(*sc.NextRunAt) {
				// If already running, skip firing to prevent overlapping runs on the same schedule
				if _, running := nextSt.ActiveRuns[id]; running {
					continue
				}

				// Compute next run time
				nextRun, err := NextCronTime(sc.Cron, now)
				if err != nil {
					continue
				}

				runAt := now
				sc.LastRunAt = &runAt
				sc.NextRunAt = &nextRun
				nextSt.Schedules[id] = sc

				cmds = append(cmds, CmdStartScheduledAgent{
					ScheduleID:  sc.ID,
					TemplateID:  sc.TemplateID,
					RepoID:      sc.RepoID,
					TriggeredAt: now,
				})
				cmds = append(cmds, CmdUpdateScheduleTimes{
					ScheduleID: sc.ID,
					LastRunAt:  sc.LastRunAt,
					NextRunAt:  sc.NextRunAt,
				})
			}
		}

	case InputTriggerManual:
		sc, ok := nextSt.Schedules[input.ScheduleID]
		if !ok {
			return st, nil, fmt.Errorf("%w: %s", ErrScheduleNotFound, input.ScheduleID)
		}
		runAt := input.Now
		sc.LastRunAt = &runAt
		if sc.Cron != "" {
			if next, err := NextCronTime(sc.Cron, input.Now); err == nil {
				sc.NextRunAt = &next
			}
		}
		nextSt.Schedules[sc.ID] = sc

		cmds = append(cmds, CmdStartScheduledAgent{
			ScheduleID:  sc.ID,
			TemplateID:  sc.TemplateID,
			RepoID:      sc.RepoID,
			TriggeredAt: input.Now,
		})
		cmds = append(cmds, CmdUpdateScheduleTimes{
			ScheduleID: sc.ID,
			LastRunAt:  sc.LastRunAt,
			NextRunAt:  sc.NextRunAt,
		})

	case InputEnableSchedule:
		sc, ok := nextSt.Schedules[input.ScheduleID]
		if !ok {
			return st, nil, fmt.Errorf("%w: %s", ErrScheduleNotFound, input.ScheduleID)
		}
		sc.Enabled = true
		sc.NextRunAt = &input.NextRun
		nextSt.Schedules[sc.ID] = sc
		cmds = append(cmds, CmdUpdateScheduleTimes{
			ScheduleID: sc.ID,
			LastRunAt:  sc.LastRunAt,
			NextRunAt:  sc.NextRunAt,
		})

	case InputDisableSchedule:
		sc, ok := nextSt.Schedules[input.ScheduleID]
		if !ok {
			return st, nil, fmt.Errorf("%w: %s", ErrScheduleNotFound, input.ScheduleID)
		}
		sc.Enabled = false
		sc.NextRunAt = nil
		nextSt.Schedules[sc.ID] = sc
		cmds = append(cmds, CmdUpdateScheduleTimes{
			ScheduleID: sc.ID,
			LastRunAt:  sc.LastRunAt,
			NextRunAt:  nil,
		})

	case InputAgentStarted:
		nextSt.ActiveRuns[input.ScheduleID] = input.AgentID

	case InputAgentFinished:
		delete(nextSt.ActiveRuns, input.ScheduleID)
		if sc, ok := nextSt.Schedules[input.ScheduleID]; ok && input.NextRun != nil {
			sc.NextRunAt = input.NextRun
			nextSt.Schedules[input.ScheduleID] = sc
			cmds = append(cmds, CmdUpdateScheduleTimes{
				ScheduleID: sc.ID,
				LastRunAt:  sc.LastRunAt,
				NextRunAt:  sc.NextRunAt,
			})
		}
	}

	return nextSt, cmds, nil
}
