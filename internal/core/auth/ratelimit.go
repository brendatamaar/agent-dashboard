package auth

import "time"

// RateLimiterConfig defines rate limiting parameters.
type RateLimiterConfig struct {
	MaxAttempts    int
	AttemptWindow  time.Duration
	LockoutDuration time.Duration
}

// DefaultRateLimiterConfig returns secure defaults (5 attempts within 15m, 15m lockout).
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		MaxAttempts:     5,
		AttemptWindow:   15 * time.Minute,
		LockoutDuration: 15 * time.Minute,
	}
}

// IPRecord tracks attempts and lockout status for an IP.
type IPRecord struct {
	Attempts    []time.Time
	LockedUntil time.Time
}

// LimiterState is the pure state of the rate limiter.
type LimiterState struct {
	Config  RateLimiterConfig
	Records map[string]IPRecord
}

// NewLimiterState initializes an empty rate limiter state.
func NewLimiterState(cfg RateLimiterConfig) LimiterState {
	return LimiterState{
		Config:  cfg,
		Records: make(map[string]IPRecord),
	}
}

// LimiterInput is the sum type of inputs to the rate limiter state machine.
type LimiterInput interface {
	isLimiterInput()
}

// CheckAllowed queries if an attempt is currently permitted.
type CheckAllowed struct {
	Key string
	Now time.Time
}

func (CheckAllowed) isLimiterInput() {}

// RecordAttempt records a login attempt result.
type RecordAttempt struct {
	Key     string
	Success bool
	Now     time.Time
}

func (RecordAttempt) isLimiterInput() {}

// CleanupPrune removes expired attempts and lockouts to manage memory.
type CleanupPrune struct {
	Now time.Time
}

func (CleanupPrune) isLimiterInput() {}

// LimiterCommand describes effects or outcomes resulting from a step.
type LimiterCommand interface {
	isLimiterCommand()
}

// CheckResult contains whether the key is allowed to attempt login.
type CheckResult struct {
	Key            string
	Allowed        bool
	RemainingTries int
	RetryAfter     time.Duration
}

func (CheckResult) isLimiterCommand() {}

// LockoutTriggered indicates a key has exceeded max attempts and is now locked.
type LockoutTriggered struct {
	Key         string
	LockedUntil time.Time
}

func (LockoutTriggered) isLimiterCommand() {}

// Step is the pure state transition function: (state₂, commands) = Step(state₁, input)
func Step(state LimiterState, input LimiterInput) (LimiterState, []LimiterCommand) {
	// Deep copy records map to guarantee immutability
	newRecords := make(map[string]IPRecord, len(state.Records))
	for k, v := range state.Records {
		attCopy := make([]time.Time, len(v.Attempts))
		copy(attCopy, v.Attempts)
		newRecords[k] = IPRecord{
			Attempts:    attCopy,
			LockedUntil: v.LockedUntil,
		}
	}
	nextState := LimiterState{
		Config:  state.Config,
		Records: newRecords,
	}

	switch in := input.(type) {
	case CheckAllowed:
		rec, exists := nextState.Records[in.Key]
		if !exists {
			return nextState, []LimiterCommand{
				CheckResult{
					Key:            in.Key,
					Allowed:        true,
					RemainingTries: state.Config.MaxAttempts,
					RetryAfter:     0,
				},
			}
		}

		if in.Now.Before(rec.LockedUntil) {
			return nextState, []LimiterCommand{
				CheckResult{
					Key:            in.Key,
					Allowed:        false,
					RemainingTries: 0,
					RetryAfter:     rec.LockedUntil.Sub(in.Now),
				},
			}
		}

		// Prune attempts outside window
		validAttempts := pruneAttempts(rec.Attempts, in.Now, state.Config.AttemptWindow)
		rec.Attempts = validAttempts
		rec.LockedUntil = time.Time{}
		nextState.Records[in.Key] = rec

		remaining := state.Config.MaxAttempts - len(validAttempts)
		if remaining < 0 {
			remaining = 0
		}
		return nextState, []LimiterCommand{
			CheckResult{
				Key:            in.Key,
				Allowed:        true,
				RemainingTries: remaining,
				RetryAfter:     0,
			},
		}

	case RecordAttempt:
		rec := nextState.Records[in.Key]
		var commands []LimiterCommand

		if in.Success {
			// Clear record on successful login
			delete(nextState.Records, in.Key)
			return nextState, commands
		}

		// On failure: prune old attempts, add new failure
		validAttempts := pruneAttempts(rec.Attempts, in.Now, state.Config.AttemptWindow)
		validAttempts = append(validAttempts, in.Now)
		rec.Attempts = validAttempts

		if len(validAttempts) >= state.Config.MaxAttempts {
			rec.LockedUntil = in.Now.Add(state.Config.LockoutDuration)
			commands = append(commands, LockoutTriggered{
				Key:         in.Key,
				LockedUntil: rec.LockedUntil,
			})
		}
		nextState.Records[in.Key] = rec
		return nextState, commands

	case CleanupPrune:
		for k, rec := range nextState.Records {
			validAttempts := pruneAttempts(rec.Attempts, in.Now, state.Config.AttemptWindow)
			if len(validAttempts) == 0 && !in.Now.Before(rec.LockedUntil) {
				delete(nextState.Records, k)
			} else {
				rec.Attempts = validAttempts
				nextState.Records[k] = rec
			}
		}
		return nextState, nil

	default:
		return state, nil
	}
}

func pruneAttempts(attempts []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	var kept []time.Time
	for _, t := range attempts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}
