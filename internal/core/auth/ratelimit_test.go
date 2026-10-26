package auth_test

import (
	"testing"
	"time"

	"agent-dashboard/internal/core/auth"
)

func TestRateLimiterStep(t *testing.T) {
	cfg := auth.RateLimiterConfig{
		MaxAttempts:     3,
		AttemptWindow:   10 * time.Minute,
		LockoutDuration: 15 * time.Minute,
	}
	state := auth.NewLimiterState(cfg)
	baseTime := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	key := "192.0.2.1"

	// Initially allowed
	var cmds []auth.LimiterCommand
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: baseTime})
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	res := cmds[0].(auth.CheckResult)
	if !res.Allowed || res.RemainingTries != 3 {
		t.Fatalf("expected allowed with 3 tries, got allowed=%v, remaining=%d", res.Allowed, res.RemainingTries)
	}

	// 1st failed attempt
	state, _ = auth.Step(state, auth.RecordAttempt{Key: key, Success: false, Now: baseTime})
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: baseTime.Add(1 * time.Minute)})
	res = cmds[0].(auth.CheckResult)
	if !res.Allowed || res.RemainingTries != 2 {
		t.Fatalf("expected allowed with 2 tries, got remaining=%d", res.RemainingTries)
	}

	// 2nd failed attempt
	state, _ = auth.Step(state, auth.RecordAttempt{Key: key, Success: false, Now: baseTime.Add(2 * time.Minute)})
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: baseTime.Add(3 * time.Minute)})
	res = cmds[0].(auth.CheckResult)
	if !res.Allowed || res.RemainingTries != 1 {
		t.Fatalf("expected allowed with 1 try, got remaining=%d", res.RemainingTries)
	}

	// 3rd failed attempt -> triggers lockout
	state, cmds = auth.Step(state, auth.RecordAttempt{Key: key, Success: false, Now: baseTime.Add(4 * time.Minute)})
	if len(cmds) != 1 {
		t.Fatalf("expected LockoutTriggered command, got %d commands", len(cmds))
	}
	lockCmd := cmds[0].(auth.LockoutTriggered)
	expectedLockout := baseTime.Add(4*time.Minute + 15*time.Minute)
	if !lockCmd.LockedUntil.Equal(expectedLockout) {
		t.Fatalf("expected locked until %v, got %v", expectedLockout, lockCmd.LockedUntil)
	}

	// Check while locked
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: baseTime.Add(5 * time.Minute)})
	res = cmds[0].(auth.CheckResult)
	if res.Allowed {
		t.Fatal("expected locked out, but CheckResult allowed=true")
	}

	// Check after lockout expired
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: baseTime.Add(20 * time.Minute)})
	res = cmds[0].(auth.CheckResult)
	if !res.Allowed {
		t.Fatal("expected allowed after lockout expired")
	}
}

func TestRateLimiterSuccessResets(t *testing.T) {
	cfg := auth.DefaultRateLimiterConfig()
	state := auth.NewLimiterState(cfg)
	now := time.Now()
	key := "user-alice"

	// 2 failures
	state, _ = auth.Step(state, auth.RecordAttempt{Key: key, Success: false, Now: now})
	state, _ = auth.Step(state, auth.RecordAttempt{Key: key, Success: false, Now: now.Add(time.Second)})

	// 1 success
	state, _ = auth.Step(state, auth.RecordAttempt{Key: key, Success: true, Now: now.Add(2 * time.Second)})

	// Check
	var cmds []auth.LimiterCommand
	state, cmds = auth.Step(state, auth.CheckAllowed{Key: key, Now: now.Add(3 * time.Second)})
	res := cmds[0].(auth.CheckResult)
	if res.RemainingTries != cfg.MaxAttempts {
		t.Fatalf("expected reset to max attempts %d, got %d", cfg.MaxAttempts, res.RemainingTries)
	}
}
