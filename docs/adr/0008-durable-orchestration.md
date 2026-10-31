# 0008 — Durable orchestration, replay and physical capacity

Accepted 2026-10-30 as part of the authorized audit remediation.

The original interpreter published state before SQLite writes finished, started
independent asynchronous command batches, and reconstructed queued agents from
incomplete rows. A failed write could still start a process; a restart could lose
the prompt/FIFO, repeat input, or free capacity before a stopped process died.

Agent and scheduler transitions now share one serialized writer. SQLite commits
the complete manager (including runnable specs, queue order, pending approvals,
allowed tools and producer cursors), scheduler reservations, agent projections,
events, approval decisions, audit entries and an ordered command outbox in one
transaction. An expected version rejects competing writes without leaving rows
behind. The in-memory state and WebSocket stream publish only after commit.
No new dependency is required.

One command interpreter processes durable effects in order. Worktree creation,
runtime startup and destruction reconcile their stable identities on replay.
Each default branch, worktree path and tmux handle has an independent run ID;
an explicitly chosen branch name is preserved. Existing worktree paths require
the managed ownership marker and matching source/branch. A resumed agent retains
its worktree and harness session ID and receives a fresh runtime handle.

Send-input, interrupt and notification delivery are non-idempotent. Once marked
started, their outcome can be unknown after a crash. They are never blindly
resent; uncertain input/interrupt delivery creates a visible system message.
Approval resolutions are persisted first; a reconnecting hook can retrieve its
prior decision or rejoin the same pending request. These local capabilities do
not need a generic multi-worker lease/queue infrastructure: one dashboard process
and its single command interpreter own this personal SQLite deployment.

Stopping or receiving a task-complete event reserves the physical capacity slot
until the runtime acknowledges destruction/exit. Queued work cannot overtake
that acknowledgment. Delete waits for destruction before removing its exact
worktree. Late provisioning/output results cannot restart cancelled or different
runtime attempts. Schedule reservation and its queued agent commit together;
only the registered run's completion clears the reservation.

Runtime output is a durable, ordered replay source separate from PTY input.
The per-run count of successfully parsed producer events commits with the event
and next state. Reopening the source skips the committed prefix. Commit/read
failures reopen the source; the monitor waits for final output persistence before
recording exit. Inspection failures are retried instead of invented as successful
exit. Dashboard shutdown cancels observers and delivery without killing sessions.

The API stream sequence is independent of harness counters and covers user and
approval events too. SQLite allocates it transactionally; pages default to 200
and cap at 500. The newest 10,000 events per agent remain available, and the
cursor survives retention/restart. Legacy duplicate sequences are renumbered
above prior cursor values in insertion order. Completed outbox rows retain a
bounded diagnostic tail. A slow WebSocket subscriber is closed so it reconnects
and replays instead of silently dropping events.

Structured event/approval values and audit strings are scrubbed before storage
and broadcast, including registered credentials that contain JSON punctuation.
Only credentials for the selected harness are injected. Each runtime receives
an independent hook token; only its hash is stored and its ownership is checked
against an active agent. The runtime installs the fixed Claude PreToolUse bridge
and fails closed if the callback is unreachable. Container callbacks require a
reachable Tailscale HTTPS origin; loopback is not a container-accessible endpoint.

Legacy queued/provisioning rows did not contain the original prompt, so that
information cannot be reconstructed during upgrade. Import marks those tasks
failed with a visible resubmit instruction and preserves their worktrees. Running
legacy sessions are reattached and pending approvals are imported. All new runs
use the complete durable snapshot.

Live tmux/Docker restart behavior, provider CLI hooks and phone notifications
still require the documented Linux/provider/phone acceptance checks. Unit tests
exercise the pure capacity/approval/resume guards, real SQLite transaction and
cursor contracts, and shell commit-failure/replay/redaction/unknown-outcome paths.


## Regression and mutation evidence

The focused core, SQLite and orchestration suites pass. These targeted mutations
were made only in a repo-local scratch copy, and each produced its expected
assertion failure. The working tree was never mutated by the experiment.

| Removed or weakened guard | Regression | Result |
| --- | --- | --- |
| version_guard | `TestDurableTransitionContract` | Caught |
| approval_owner | `TestApprovalOwnershipAndPerAgentCount` | Caught |
| physical_stop_slot | `TestPhysicalSlotHeldUntilStopAcknowledged` | Caught |
| structured_scrub | `TestStructuredSecretsScrubBeforePersistenceAndBroadcast` | Caught |
| producer_cursor | `TestReplayCursorSurvivesRestart` | Caught |
| unknown_delivery | `TestStartedInputHasUnknownOutcomeAndIsNotRetried` | Caught |
| singleton_guard | `TestInitialUserAtomicSingleton` | Caught |
| outbox_retention | `TestCompletedOutboxRetentionPreservesUnfinishedEffects` | Caught |

Additional passing regressions cover final output persistence failure followed by
replay before completion, approval-ID reuse with a changed command, atomic rollback
on a late SQL failure, deterministic recurring-schedule reservations/completion,
resumed session/worktree identity, the legacy missing-prompt migration, and random
physical-capacity/approval sequences with a deterministic seed and shrinker.

The targeted mutation budget covered the eight highest-risk durable/security
guards above. The remaining new clauses were exercised by their positive/negative
regressions rather than additional mutations. Live runtime/provider contracts
remain gated on the supported Linux environment.
