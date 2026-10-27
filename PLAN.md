# PLAN.md — agent-dashboard

Build order for `PRD.md`. Work one phase at a time, tick tasks as they are done, and stop at each gate for the user to test and confirm. Read `AGENTS.md` first.

Legend: `[ ]` todo · `[x]` done · **Spike** = a short investigation whose result is written to `docs/adr/` before building on it.

## Ground rules for every phase

- Design the state machines first (states, transitions, commands) and review them with the user before coding the shell around them.
- Core logic is pure; effects live in the shell. Property-test the invariants.
- `make verify` must be green before a phase is called done.
- End each phase with a short report: what changed, what to test on the phone, known gaps.

## Phase 0 — Foundation

Goal: a secure empty shell the user can open from their phone over Tailscale.

- [x] Initialise repo layout from `AGENTS.md`; `Makefile` with `dev`, `test`, `lint`, `build`, `verify`.
- [x] **Spike:** pick libraries (SQLite driver, router, migrations, WebAuthn, TOTP, encryption, WebSocket, PTY). Prefer the standard library and a few well-maintained packages. Record choices in `docs/adr/0001-libraries.md`.
- [x] Go server skeleton: config file + env, structured logging, `/healthz`, graceful shutdown.
- [x] Bind policy: listen only on the Tailscale interface or `127.0.0.1`; refuse `0.0.0.0` without an explicit flag. Test it.
- [x] SQLite store with migrations; tables from the PRD data model that Phase 0 needs (`secrets`, `audit_log`, sessions/credentials).
- [x] Auth: first-run setup (create passkey, set password + TOTP fallback), login, short-lived sessions, CSRF, login rate limit.
- [x] Encrypted secrets store (key derived from a master passphrase or an age key); no plaintext secrets on disk or in logs.
- [x] Audit log: write helper plus an API to read it.
- [x] React + TS + Vite app, embedded with `go:embed`; PWA manifest and service worker; login screen; empty home screen; system panel (CPU, RAM, disk) from the backend.
- [x] `scripts/install.sh` and a systemd unit running as user `agentd`; README with Tailscale + swap-file setup notes.

**Gate 0:** the user logs in from their phone over Tailscale, adds the app to the home screen, and confirms nothing listens on a public port (`ss -tlnp` shows only the Tailscale or loopback address).

## Phase 1 — Repos and Claude Code on the host runtime

Goal: start, watch and stop a Claude Code agent on a repo from the phone, with tmux underneath.

- [x] **Spike (Claude Code):** read `claude --help` and the current docs for the installed version. Decide the event source for each mode and write it to `docs/adr/0002-claude-events.md`:
  - task mode: `claude -p` with streaming JSON output on stdout;
  - interactive mode: the TUI runs inside tmux; events come from hooks and/or by tailing the session transcript file; the raw terminal view is always the fallback.
  Also decide how sessions are resumed (`--resume` or equivalent).
- [x] **Spike (auth):** how Claude Code authenticates on a headless server with a Claude subscription token or API key, and where credentials live. Document the setup steps in the README. Remind the user to check provider terms for subscription use on servers.
- [x] Core: agent lifecycle state machine and task queue (cap default 1, max 2), with property tests. Normalized event types.
- [x] Repos: add by GitHub URL using the fine-grained token (stored encrypted), clone to `~/workspace/<repo>`, list with branch / ahead-behind / dirty state, fetch and pull. Validate repo and branch names.
- [x] Worktrees: create `agent/<slug>` worktree per agent from a chosen base branch; remove on delete.
- [x] `Runtime` interface plus the `host` driver: tmux session per agent, as user `agentd`.
- [x] `Harness` interface plus the `claude` adapter (start, resume, parse events, stop). Fixture tests from recorded real output.
- [x] Re-attach on boot: on restart, rediscover running tmux sessions and rebuild state from the database.
- [x] API + WebSocket event stream; web terminal (xterm.js) attached to an agent's tmux session; plain host shell.
- [x] UI (phone-first): agent cards (needs me, running, done), start-agent form (repo, branch, prompt, mode), agent detail with live view, send message, interrupt, stop, resume, delete.

**Gate 1:** from the phone the user starts a Claude task on a repo, watches it, sends a follow-up, closes the browser, then attaches to the same session over SSH (`tmux attach`) and sees it still running. Restarting the dashboard does not kill the agent.

## Phase 2 — Git loop, approvals, notifications

Goal: review and ship from the phone.

- [x] Git service (run `git` with argument arrays, never shell strings): status, changed-file tree, unified diff, stage/unstage (file and hunk), commit, amend, branch create/switch/delete, push with upstream, pull/rebase onto base.
- [x] Push and PR with the dashboard's token only; agents never see it. Open a PR with title and body, body drafted from the agent's summary.
- [x] Guardrails: protected-branch list, no force-push, confirmation dialog for pushing `main`. Tests for each refusal.
- [x] **Spike (approvals):** compare a `PreToolUse`-style hook that calls the dashboard and waits, against the headless permission-prompt option, for surfacing Claude Code permission prompts. Pick one, document it in `docs/adr/0004-approvals.md`, and add a fallback: if a prompt cannot be intercepted, notify "needs input" and open the terminal.
- [x] Approval cards in the UI (command, file, diff) with Approve / Deny / Always allow for this agent; approvals table and audit entries.
- [x] Web Push (VAPID keys generated at setup) for: needs approval, finished, failed, idle too long; notifications deep-link to the agent card.
- [x] Phone UI polish: unified diff view, commit sheet, one-handed actions, terminal extra-key row (Esc, Tab, Ctrl, arrows).

**Gate 2:** end to end from the phone: an agent asks for permission → push arrives → approve → agent finishes → push arrives → review the diff → commit → push → PR opened. No credentials appear in agent logs.

## Phase 3 — Docker runtime and parallel agents

Goal: safe parallelism on 2 GB, with Docker as the default runtime.

- [x] `docker` runtime driver: image with tmux, git, Node and the harness CLIs; one container per agent; only the worktree mounted; non-root user; no Docker socket; no host ports; dropped capabilities; CPU and memory limits (about 700 MB each).
- [x] Secrets injected per container at start; verify they never reach the worktree or logs.
- [x] Per-repo settings: runtime, image, setup command, env vars, allowed tools. Repos that need `docker compose` use the `host` runtime.
- [x] Docker becomes the default runtime; auto-approve allowed only here, off by default.
- [x] Concurrency: global and per-repo caps, queue, queued state in the UI.
- [x] Resource panel: per-agent CPU/RAM, disk usage with an 80% warning, swap status; OOM detection with a clear failure state.
- [x] Contract test suite shared by `host` and `docker` runtimes.

**Gate 3:** two agents run in parallel on two repos within 2 GB, a third task queues, none of them can reach host services or each other's worktrees, and killing one container leaves the other and the dashboard unaffected.

## Phase 4 — More harnesses and automation

Goal: Codex, Gemini and Muse behind the same adapter, plus templates and schedules.

- [x] **Spike per harness:** read `--help` for the installed version; record headless command, resume command, auth method and approval behaviour in `docs/adr/`.
- [x] Adapters: Codex CLI (`exec`), Gemini CLI, Muse Code (`exec`, `resume`). Each passes the shared harness contract tests with recorded fixtures.
- [x] Harness manager: installed version, update button, auth status and expiry per harness.
- [x] Templates (saved prompt + settings) and scheduled tasks (cron-style) that queue agents.
- [x] Subagent view: show Claude Code subagent activity nested under its parent; manage `.claude/agents/*.md` per repo.
- [x] Optional channels: Telegram or ntfy notifications; quiet hours.

**Gate 4:** the user runs one task each on Claude Code, Codex and Gemini (and Muse if they have a key) from the same UI, and a scheduled nightly task opens a PR.

## Later (not in v1)

Multi-repo agents; agent-to-agent handoff; PR status and CI checks; commit log and revert; conflict view with "resolve with agent"; Arcana as a runtime.

## Open spikes to resolve early

| Spike | Phase | Decides |
| --- | --- | --- |
| Library choices | 0 | Dependencies and licences |
| Claude event source per mode | 1 | How live view works without parsing a TUI |
| Headless auth with a Claude subscription | 1 | How the token is stored and renewed |
| Approval interception | 2 | Whether approve/deny can work from the phone for each harness |
