# Audit remediation — Model Functional Effect design
Profile: distributed (agent persistence and recoverable effects); service for HTTP and UI boundaries.

The user authorized all audit fixes on 2026-10-30. This repairs existing phases;
the five proposed product additions remain a separate backlog.

## State map
Agent: queued → provisioning → running ⇄ awaiting_approval → idle → stopped/done/failed.
Resume is an explicit transition from a terminal state, preserving the worktree.
Provisioning and external effects may be pending, completed, failed, or require reconciliation.
Schedule: ready → active(run ID) → ready after that run terminates.
Event view: disconnected → connecting → replaying(cursor) → live → disconnected.
Secrets: locked/unconfigured or available under an independent master key; never derive a key from database contents.

## Inputs and transitions
| From | Input and guard | To | Commands |
| --- | --- | --- | --- |
| Any agent | Valid action/result for this agent and attempt | Legal next state | Persist transition and recoverable commands before effects |
| Awaiting approval | Matching pending approval owned by this agent | Running only if no other requests for this agent | Persist decision, resolve request, audit |
| Active schedule | Its agent terminates | Ready | Persist completion and next occurrence |
| Recovered agent | Runtime/output inspected | Reattached, reconciled, or explicit failure | Resume streams/monitoring; never silently report unknown exit as success |
| Any event view | Page/live event with seq > cursor | Same view, advanced cursor | Deduplicate and retain bounded visible events |
| Terminal agent | Explicit resume, capacity permits | Provisioning | Persist and launch harness resume against preserved worktree |

Other illegal inputs return a domain error without changing state or issuing commands.

## Invariants and test map
| Invariant/boundary/capability | Tests |
| --- | --- |
| Existing work is never deleted by provisioning | Repeated prompt/path and invalid-base regression; independent run identities |
| Credentials never enter repo files, transcripts, or unrelated process environments | Canary clone-config, origin-change refusal, encrypted notifier storage, structured/error redaction, environment allowlist |
| Effects occur only after durable commit, in transition order | Store failure, reversed/concurrent transition, crash/recovery tests |
| Recovery preserves runnable spec and FIFO; completion releases slots | Restart with queued + running agents, stream replay and actual exit tests |
| Approval ownership and pending count are per agent | Two-agent approval table/property tests, stale/duplicate decision tests |
| Every schedule can run repeatedly | Finish first run, tick next occurrence, restart active run |
| Runtime contracts cover input, replayable output, real exit outcome and cancellation | Shared fake + gated live host/Docker clauses |
| HTTP boundaries reject malformed/oversized/untrusted input before effects | Client-IP, origin, hook auth, singleton setup, JSON/cron/path confinement tests |
| Pure core performs no timezone/filesystem/network effects | Mechanical purity scan plus deterministic quiet-hour tests |
| UI replay cannot lose, duplicate, or mix agent histories | Pure merge/cursor/reconnect tests; deep-link and settings defaults tests |

## Commands and cardinality
| Command | Cardinality | Idempotence/result |
| --- | --- | --- |
| Persist transition + pending effects | One transaction | Expected version / serialized writer; failure prevents effects |
| CreateWorktree / StartSession | One per run ID | Reconcile existing identity before retry; mismatched existing path is refused |
| SendInput / ResolveApproval | One | Never blindly retry an unknown non-idempotent outcome |
| ReadOutput | Stream | Replay stable source events and deduplicate persisted cursor |
| Inspect runtime / fetch event page | One bounded snapshot | Safe to retry |
| KillSession / cleanup | One per handle | Idempotent; cleanup never removes another run's path |
| NotifyUser | One | Best effort, no automatic non-idempotent retry |

## Call graph
```go
HTTP → validated boundary → serialized transition
  → agent.Step (core) → atomic durable state/commands
  → command interpreter → Git / Runtime / encrypted store / notifier
  → result input → serialized transition
Boot → load complete state + pending effects → inspect/reconcile → reattach output/monitor
Runtime output → harness parser → redaction → dedup → core → durable event → WebSocket
Scheduler tick + agent completion → scheduler.Step → durable schedule → submit run
Browser connect → bounded event replay → sequence merge → live stream → reconnect on close
```
Tests replace external capabilities with shared-contract fakes/simulators; SQLite stays real.

## Failures
| Failure | Class | Handling |
| --- | --- | --- |
| Invalid action, malformed input, unavailable capability | domain outcome | Reject explicitly; no effects |
| Read/transient inspection failure | retry | Bounded retry/cancellation; do not invent successful exits |
| Unparseable harness stream | escape hatch | Preserve raw terminal and surface warning |
| Commit failure / unknown create outcome | escape hatch | Keep durable reconciliation work; stop dependent effects |
| Broken invariant | die | Return/report internal error; never swallow it as successful work |

## Requirements and boundaries
SQLite transition store, Git argv adapter, runtime driver, harness parser, encrypted secret
store, notifier, clock/ID provider and broadcaster are shell capabilities. User requests,
runtime handles/results, stored JSON and harness bytes are validated at their boundaries.
Origin and hook authentication precede PTY creation or agent state changes. Session teardown
is separate from dashboard shutdown, with a bounded HTTP/WebSocket shutdown path.

## Files and acceptance
Changes span existing core/shell/store/runtime/harness/API/web packages and deployment scripts.
Each owner records focused regression results and scratch-copy mutation evidence. Final checks:
purity scan, make verify, dependency audit, Linux cross-build and gated runtime contracts.
No host deployment, destructive cleanup, provider messaging, or real credentials are required.

## Open questions
None block the authorized bug fixes. Live Linux, phone and provider checks must be reported
separately when unavailable. Proposed preflight/recovery/budget/brief/cleanup product additions
are future work, not a substitute for repairing the existing implementation.


## Orchestration implementation coverage

Distributed-profile coverage uses the real SQLite store and the runtime fake
that also participates in the runtime contract suite.

| Requirement | Named regression/property | Status |
| --- | --- | --- |
| Atomic durable state/events/outbox and single expected-version winner | `TestDurableTransitionContract` | Passing |
| Complete runnable spec and FIFO round trip | `TestDurableTransitionContract` | Passing |
| Physical capacity held through stop/done and cancellation guards | `TestPhysicalSlotHeldUntilStopAcknowledged`, `TestDoneReservesSlotUntilPhysicalExit`, `TestPropertyPhysicalCapacityAndApprovalOwnership` | Passing |
| Approval ownership, per-agent count, late request and replay binding | `TestApprovalOwnershipAndPerAgentCount`, `TestApprovalReplayCannotChangeAuthorizedCommand` | Passing |
| Resume preserves worktree/provider session and uses a new runtime handle | `TestResumePreservesWorktreeAndHarnessSession`, `TestInteractiveHookSessionIdentityIsDurable` | Passing |
| Repeated schedules reserve before dispatch and fence completion | `TestScheduleReservationAndFencedCompletion` | Passing |
| Commit failure prevents runtime input and event publication | `TestFailedCommitDoesNotPublishOrRunEffects` | Passing |
| Raw source cursor survives restart; final commit retries before exit | `TestReplayCursorSurvivesRestart`, `TestFinalOutputCommitRetriesBeforeCompletion` | Passing |
| Structured custom credential redaction before persistence/broadcast | `TestStructuredSecretsScrubBeforePersistenceAndBroadcast` | Passing |
| Unknown non-idempotent input outcome is visible and never resent | `TestStartedInputHasUnknownOutcomeAndIsNotRetried` | Passing |
| Sequence paging/retention and completed outbox retention | `TestEventSequencePagingAndRetention`, `TestCompletedOutboxRetentionPreservesUnfinishedEffects` | Passing |
| Competing first-run administrators cannot both win | `TestInitialUserAtomicSingleton` | Passing |
| Legacy missing prompt fails explicitly without destructive recovery | `TestLegacyQueuedSpecIsNotInventedDuringUpgrade` | Passing |

Eight targeted scratch-copy mutants were caught (version, ownership, physical
slot, nested scrub, producer cursor, unknown delivery, singleton and completed
outbox retention). Detailed evidence and delivery semantics are in ADR 0008.
Live tmux/Docker/provider acceptance remains gated on Linux and actual CLI
credentials; automated shell tests do not claim that live acceptance.
