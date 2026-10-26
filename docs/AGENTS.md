# AGENTS.md — agent-dashboard

Guidance for coding agents (Claude Code, Codex, etc.) working in this repo. Read this file, then `PRD.md` and `PLAN.md`, before changing anything.

## What this project is

`agent-dashboard` is a personal, self-hosted web dashboard that runs on a small Linux VPS (2 vCPU, 2 GB RAM). It manages git repos, coding agents (Claude Code first; Codex CLI, Gemini CLI and Meta's Muse Code later) and git operations, from a laptop or phone. Agents keep running when no one is connected.

It is single-user, not open source, and not a SaaS. Do not add multi-tenancy, billing, or teams.

## Source of truth

1. `PRD.md` — what to build and why. Do not silently deviate. If the PRD is wrong or impractical, write a short note in `docs/adr/NNNN-title.md` explaining the change, and tell the user.
2. `PLAN.md` — the order of work. Build one phase at a time. Stop at each gate and report.
3. This file — how to build it.

If these conflict, stop and ask.

## Stack

- Backend: Go (latest stable), single binary, SQLite (prefer a pure-Go driver so the binary builds without CGO), `log/slog` for logs, standard library `net/http` plus a small router.
- Frontend: React + TypeScript + Vite, mobile-first, installable PWA (service worker, Web Push), xterm.js for the terminal. Built output is embedded into the Go binary with `go:embed`.
- Runtime: tmux for every agent session; Docker (default runtime) or host (opt-in) as the isolation layer; git worktrees for parallel agents.
- Target OS: Linux (Ubuntu 24.04). Development may happen on any OS, but anything touching tmux, Docker or PTYs is Linux-only. Keep that code behind interfaces so the core and unit tests run anywhere.
- Add a dependency only when it clearly saves real work; prefer the standard library. Note each non-trivial dependency and why in the relevant ADR or PR description.

## Architecture rules (Model Functional Effect)

Stateful logic uses the pattern `(state₂, commands) = f(state₁, input)`.

- `internal/core/...` is the functional core: pure Go, no IO, no clock, no randomness, no goroutines, no `os`/`exec`/`net`. Time and ids come in as inputs. It returns the next state and a list of commands (plain data).
- `internal/shell/...` (and adapters) execute commands: run tmux, call Docker, run git, write SQLite, send push notifications. Shell code has no business logic beyond translating a command into an effect and the result back into an input event.
- Persist each state transition before running its side effects, so a crash or restart leaves a recoverable state.
- The agent lifecycle and the task queue are state machines in the core: `queued → provisioning → running ⇄ awaiting_approval → idle (interactive) → stopped | done | failed`. Illegal transitions must be impossible or return an error, never panic.
- If the `functional-effect` Claude Code plugin is installed, use its skills: `/functional-effect:design` before a stateful feature, `/functional-effect:test` for tests, `/functional-effect:check` before finishing. If it is not installed, follow the rules above by hand.

### Two extension points

```go
// One implementation per CLI (claude, codex, gemini, muse).
type Harness interface {
    Name() string
    Check(ctx context.Context) (Version, AuthStatus, error)
    StartCommand(spec AgentSpec) []string
    ResumeCommand(spec AgentSpec, sessionID string) []string
    ParseEvents(r io.Reader) <-chan Event
    Respond(a Approval) []byte
}

// One implementation per isolation strategy (docker, host; maybe Arcana later).
type Runtime interface {
    Create(ctx context.Context, spec RuntimeSpec) (Handle, error)
    Exec(ctx context.Context, h Handle, cmd []string) (Session, error)
    Attach(ctx context.Context, h Handle) (PTY, error)
    Stats(ctx context.Context, h Handle) (Usage, error)
    Destroy(ctx context.Context, h Handle) error
}
```

The UI only knows normalized events: `message`, `tool_call`, `tool_result`, `approval_needed`, `subagent_started`, `subagent_finished`, `usage`, `done`, `error`. If a harness's output cannot be parsed, degrade to the raw terminal view; never lose the session.

Treat these signatures as a starting point. Refine them when the first real harness forces it, and record the change in an ADR.

## Security rules (non-negotiable)

This app is a remote code-execution surface. When in doubt, choose the safer option and say so.

- Bind only to the Tailscale interface or `127.0.0.1`. Refuse to start on `0.0.0.0` unless an explicit flag is set. Never open a public port.
- Auth: passkey (WebAuthn) primary, password + TOTP fallback. Short-lived sessions, CSRF protection, rate-limited login. No unauthenticated endpoints except `/healthz` (no sensitive data).
- Run as a dedicated non-root user `agentd`. Document that the `docker` group is root-equivalent.
- Containers: non-root inside, only the agent's worktree mounted read-write, no Docker socket, no host ports, CPU and memory limits, dropped capabilities.
- Secrets (harness credentials, the GitHub token) are encrypted at rest, injected per container at start, and never written to worktrees, logs, events or the audit log. Scrub known token patterns from logs and transcripts.
- Agents never receive GitHub credentials. The dashboard pushes and opens PRs with its fine-grained token.
- Auto-approve modes (`--dangerously-skip-permissions` and equivalents) are allowed only in the Docker runtime and are off by default.
- Never force-push or push to protected branches (`main` by default) without an explicit user confirmation in the UI.
- Every agent start, approval, git push and terminal session goes into the audit log.
- Never build shell commands by string concatenation with user input. Use argument arrays. Validate repo names, branch names and paths.

## Resource limits

The target VPS is small. Respect it.

- Default 1 running agent, maximum 2; extra tasks queue.
- Dashboard under about 100 MB RAM idle; each agent container capped at about 700 MB; recommend a 2–4 GB swap file.
- No polling loops tighter than necessary; use event streams. Rotate transcripts and logs. Remove worktrees of deleted agents.

## Repository layout (target)

```
cmd/agent-dashboard/      main package, wiring
internal/core/            pure state machines, planning, normalized events
internal/shell/           executes commands (tmux, docker, git, notifier, store)
internal/harness/         adapters: claude, codex, gemini, muse
internal/runtime/         drivers: docker, host
internal/api/             HTTP + WebSocket handlers, auth
internal/store/           SQLite schema, migrations, queries
web/                      React + TypeScript PWA
docs/adr/                 architecture decision records
scripts/                  install script, systemd unit, helpers
PRD.md  PLAN.md  AGENTS.md  CLAUDE.md
```

Adjust the layout if there is a good reason, and record it in an ADR.

## Commands

Phase 0 creates a `Makefile` with these targets; keep them working:

- `make dev` — run backend and frontend in dev mode
- `make test` — Go tests (race detector on) plus frontend tests
- `make lint` — `go vet`, `golangci-lint` if available, frontend lint and type-check
- `make build` — build the frontend, embed it, build the Go binary
- `make verify` — `lint` + `test` + `build`; run this before saying a phase is done

## Testing

- Core state machines: table tests for transitions plus property tests for invariants (for example: a task is never `running` in two places; the number of running agents never exceeds the cap).
- Every parser (harness event streams) gets fixture-based tests from recorded real output. Save fixtures under `testdata/`.
- Every `Runtime` and `Harness` implementation passes a shared contract test suite, with fakes for unit tests. Integration tests that need Docker or tmux are tagged and skipped when unavailable.
- Test that illegal states and security checks fail: bind-address refusal, protected-branch push refusal, secret scrubbing.
- A test you have not seen fail may not test anything; break the code once to confirm.

## Conventions

- Go: `gofmt`, small packages, errors wrapped with `%w`, `context.Context` first, no global state, no `panic` for expected failures.
- TypeScript: strict mode, no `any` without a comment, components small and phone-first (test at 380 px wide).
- Commits: Conventional Commits (`feat:`, `fix:`, `chore:`). One logical change per commit. Work on feature branches; never commit directly to `main`.
- Write code that is easy to delete. Prefer boring and explicit over clever.
- Comments explain why, not what.

## Working agreement for agents

1. Start each session by reading `PLAN.md` and checking which tasks are done.
2. Work on the current phase only. Do not start the next phase until the user confirms the gate.
3. Tick off tasks in `PLAN.md` as you finish them, and keep notes short.
4. CLI flags and output formats of Claude Code, Codex, Gemini and Muse Code change often. Do not trust memory: run `<cli> --help` for the installed version and record what you rely on in the adapter's doc comment.
5. When something is ambiguous or risky, or the PRD seems wrong, ask the user instead of guessing.
6. Finish each phase with `make verify` green, a short summary of what changed, and what the user should test manually on their phone.
7. Never run destructive commands (`rm -rf` outside the repo, `git push --force`, `docker system prune`, anything touching the host outside this project) without asking.
