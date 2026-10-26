---
name: model
description: The Model Functional Effect pattern — (state₂, commands) = f(state₁, input) — for writing or changing stateful logic in Go or TypeScript (plain or Effect). Use when implementing a feature with states and transitions (orders, workflows, async UI state, LLM agent loops), when code mixes logic with IO, or when a project has a .functional-effect.json. Scales by profile (core, service, distributed).
---

# Model Functional Effect

```
(state₂, commands) = f(state₁, input)      // pure: the functional core
commands → interpreted at the edge → inputs // effectful: the imperative shell
```

A program is a sequence of typed states. A pure function moves from one state to the next in response to an input and *describes* the effects it wants as data (commands). A thin shell performs those commands and feeds their results back as new inputs.

## The four layers

| Layer | Question | Tool |
|---|---|---|
| 1. State map | Which states can exist? | List every state, including loading, error, empty, retrying, cancelled |
| 2. Types | How do we make invalid states unrepresentable? | Sum types for State/Input/Command, branded/value types parsed at the boundary |
| 3. Transitions | Which state → state moves are legal? | One `step` function; its cases are the transition table |
| 4. Exploration | Which sequences did nobody think of? | Property tests: random input sequences, invariants checked after every step |

## Profiles: apply as much as the project needs

`profile` in `.functional-effect.json` (default `service`) sets how much of this skill applies: `core` (pure `step`, sum types, property tests), `service` (+ boundary, interpreter, failure classes, capabilities, contract tests), `distributed` (+ outbox, inbox, timers, unknown outcomes with fencing, replay, chaos tests). Sections below are marked with the profile they start at. Do not add an outbox to a CLI or a fence to a read-only call; see [references/profiles.md](references/profiles.md), and suggest raising the profile when a feature crosses a line (it starts moving money, sending mail, or calling something that cannot be retried).

## Rules for core files (all profiles)

The core is the path set in `.functional-effect.json` (`core`), commonly `internal/core/**` or `src/core/**`.

- Export `step(state, input) → (state, commands)` and a `run` fold over a list of inputs.
- `State`, `Input` and `Command` are closed sum types. Go: interfaces with an unexported marker method and a `//sumtype:decl` comment (checked by `gochecksumtype`). TS: discriminated unions with a `tag` field. Switches over them handle every variant explicitly; no catch-all `default` that would swallow a new variant.
- A plain status whose values carry no data of their own (`queued`, `running`, `done`) can stay a named enum: Go `type TaskStatus string` with constants and a `Validate`/parse function, TS a union of string literals. Use a sum type as soon as one variant carries data the others do not (an attempt, a reason, a tracking number). Never a raw `string` or a set of booleans.
- No IO, network, filesystem, database, console/logging, clock, randomness, timers, goroutines, environment variables, or `Effect.run*`. Randomness and generated IDs enter as fields on an `Input`.
- **Time is a command.** To wait, emit `ScheduleInput{after, input}`; the shell feeds `input` back when it is due (e.g. `PaymentTimedOut`). Never read the clock to decide.
- **Idempotency keys are derived in the core** from state (`charge:<orderId>:<attempt>`), so replays and redeliveries produce the same keys. *(service and up)*
- **Results and timers carry the attempt they belong to.** Guard on it, so a late result or a stale timer from an earlier attempt cannot move the current one. *(service and up)*
- Unhandled `(state, input)` pairs return `(state, no commands)`. Late, duplicated or out-of-order events are ignored this way.
- Values that need validation (amounts, IDs, emails) have smart constructors / branded types so an invalid value cannot exist past the boundary.
- Keep wire formats (JSON field names, `_tag`, nullable columns) out of the core; mapping them is the boundary's job.

## Rules for shell files (service and up)

- **Boundary**: every value from outside (HTTP body, webhook, queue message, DB row, env, file, LLM output) is `unknown` until parsed into a core type. Parse once, at the edge. TS: zod (or `Schema.decodeUnknownEffect` with Effect v4); Go: decode into a wire struct, then call core constructors.
- **Interpreter**: one function maps each `Command` to an effect and returns the `Input` it produces (or none). Domain outcomes become `Input` values (e.g. `PaymentFailed`); failures are handled by their class (below).
- **Capabilities** are injected, never imported globally: Go interfaces in a `Deps` struct with `context.Context`; TS a plain `Deps` object of interfaces (or, with Effect, services in the `R` of `Effect<A, E, R>`). If testing a function needs many fakes, it does too much: split it.
- **Cardinality** is decided per command: one value (Go: a call; TS: `Effect`), a stream of values over time (Go: channel/iterator in the shell; TS: `Stream`), or a time-bounded value that is cached with a TTL and deduplicated for concurrent callers.
- **Cross-cutting behavior** (retry, timeout, tracing, logging, caching) wraps the interpreter or a capability — `.pipe(...)` in Effect, middleware/decorators in Go — so the main flow stays readable.
- **Resources** (connections, files, locks) are acquired and released with their scope (`defer` in Go; `Effect.acquireRelease`/`Scope` in Effect), so release happens on error and interruption too.
- **Execution** happens once, at the entry point (`main`, the HTTP handler, the job runner): `Effect.runPromise`, or the Go/TS `main`.

## Failure classes (service and up)

Classify every way a command can fail into exactly one class:

| Class | Example | Handling |
|---|---|---|
| Domain outcome | declined, not found, out of stock, already exists | An `Input` and a state; never an error. The core decides what happens next. |
| Retry | timeout, rate limit, 503, connection reset | Retry with a schedule (backoff, max attempts) in the shell. **Only if the command is idempotent or carries an idempotency key**: a timeout does not mean the operation failed. |
| Escape hatch | cache miss on an optional enrichment, stale read allowed | Fallback value, recorded in the state or `Input` so the core knows it is degraded. |
| Die | syntax error in query, missing capability, out of memory, bug | Crash the process, close the connection, or return 500. Never catch to recover. |

## Persistence and delivery (distributed)

- **Commit state and its commands together** (transactional outbox). Handlers do not execute effects: they call `step`, commit the new state and the commands to an outbox table in one transaction, and return. A worker reads the outbox and interprets the commands.
- **Optimistic concurrency**: the state table carries a `version`. Commits assert `version = expected`; on conflict, reload state and re-run `step`.
- **Inbox deduplication**: every external message carries a producer ID. Record it before acting; duplicate messages are acknowledged without re-running `step`.
- **Unknown outcomes**: when a command times out after being sent, do not retry blindly. Fenced reconciliation queries the provider or records an `Unknown` state that requires operator or scheduled resolution.

## Call graph convention

Plain text, indented `→` arrows, in a `ts` or `go` code block:

```ts
// Production
HTTP POST /orders
  → parseInput (boundary)
  → step (core)
  → interpret ChargeCard
      → PaymentGateway.live.charge (retry: 3× backoff, key charge:<id>:<attempt>; then wait for timer)
  → interpret ResolvePayment
      → PaymentGateway.live.resolve (fenced)
  → interpret CreateShipment
      → Shipping.live.create (retry: 3× backoff, key ship:<id>; then retry later)
  → handle(<item>:result) → complete

// Tests
run(initial, inputs) (core only, property tests)
handle → MemoryStore
worker → PaymentGateway.fake, Shipping.fake (scripted failures, crash points)
```

The code should have the same shape as the graph. If it does not, either the graph or the code is wrong.

For patterns see [references/go.md](references/go.md), [references/typescript.md](references/typescript.md) (plain TypeScript: zod, `Deps`, fast-check) and [references/typescript-effect.md](references/typescript-effect.md) (Effect v4; choose it when `package.json` already depends on `effect`).

## Mapping to Effect<A, E, R> (only for projects that use Effect)

| Model | Effect |
|---|---|
| Command (description of an effect) | An `Effect` value: lazy, immutable, composable |
| Interpreter | Effect runtime / your `interpret` function |
| Domain outcome → Input | Error channel `E`, discharged with `catchTag` into a success value |
| Retry / escape hatch / die | `Effect.retry` + `Schedule`, `catchTag` with a fallback, `Effect.die` / `orDie` |
| One / stream / cached | `Effect` / `Stream` / `Effect.cachedWithTTL` or `Cache` |
| Capabilities the shell needs | Requirements channel `R`, discharged with `provide` |
| Run at the edge | `Effect.runPromise` in `main` only |
| The shell owns the clock | `Clock.currentTimeMillis` at the edge; `now` passed in below it |

## When the checker blocks an edit

A `PreToolUse` hook denies edits that add effects to core files and explains which line and why. Do not work around it (renaming, aliasing, indirection, writing the file through a shell command): the `Stop` hook re-checks every core file changed during the turn. Instead:

- Return a `Command` describing the effect and handle it in the shell, or
- Pass the needed value in through an `Input` field, or
- Move the code to a shell path.

`fe:allow <reason>` on a line marks a deliberate, reviewed exception. It is the user's decision, not yours: every new one triggers an approval prompt, and one without a reason suppresses nothing. Restructure first; propose an exception only when restructuring is clearly worse, and say why.

## When the verify command fails

If the project sets `verify` in `.functional-effect.json` and the user approved it, the `Stop` hook runs it after any turn that changed core files and returns the failing output. Treat it like a failing test: find the broken invariant or transition, fix the code (or the test if the test is wrong, and say so), and do not weaken assertions to make it pass.

## Agent workflow follows the model

- Plan before editing (plan mode or the `design` skill): the plan is the program as a value.
- Hooks and permissions are the policy check between "command" and "effect".
- Sandboxes, worktrees and checkpoints are interpreters with isolated, revertible state.
- The session transcript is the event log; resume and fork replay it.
