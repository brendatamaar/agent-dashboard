# PRD — agent-dashboard

Oct 26, 2026 · @brendatama

## Overview

A self-hosted, single-user web dashboard on my VPS (2 vCPU, 2 GB RAM) that manages repos, coding agents and git operations from a laptop or phone, with agents that keep working while I am offline. Claude Code (via my Claude subscription) is first-class; Codex CLI, Gemini CLI and Meta's Muse Code are supported through the same adapter.

**Problem.** Running agents on the VPS today means SSH + tmux by hand: no overview of what is running where, no notifications, painful diffs and approvals on a phone, and no safe way to run several agents across repos without them stepping on each other or on the services the VPS already hosts.

**Goals (v1)**

- Clone, list and sync repos on the VPS from the browser.
- Start, watch, message, pause and stop agents per repo, each in its own git worktree, and resume them after I reconnect.
- Run several agents in parallel across repos, isolated from each other and from anything else on the VPS.
- Do the everyday git loop without a terminal: status, diff, stage, commit, push, branch, open a PR.
- Get a phone notification when an agent finishes, fails or needs approval, and act on it in two taps.
- Fall back to a real terminal (web or SSH) for anything the UI does not cover.

**Non-goals (v1)**

- Multi-user, teams, roles or billing.
- Hosting the models or proxying provider APIs; each harness talks to its own provider.
- A full IDE or code editor in the browser.
- Replacing Yggdrasil (local, Windows) or Arcana (sandbox orchestrator); this is a separate project, though Arcana may later plug in as a runtime.
- CI/CD or deploying the apps it works on.

## Users and core scenarios

One user: me, a full-stack engineer who is comfortable in a terminal but wants to steer agents from a phone during the commute or away from the desk.

1. **Kick off from the laptop, check from the phone.** I start a refactor agent on repo A from the laptop, close it, and later open the dashboard on my phone to read progress and the diff.
2. **Fire and forget overnight.** I queue a task ("add tests for the payment module, open a PR"), go offline, and wake up to a notification with a PR link or a failure summary.
3. **Parallel work across repos.** Two agents run at once, the limit on today's 2 GB VPS: Claude Code on repo A (feature) and Codex on repo B (bug fix), each in its own worktree and container. A third task waits in the queue.
4. **Approve from the phone.** An agent hits a permission prompt (run a migration, install a package). I get a push, see the exact command, and tap Approve or Deny.
5. **Review and ship.** I read the diff on my phone, leave a follow-up instruction or commit, push and open a PR from the dashboard.
6. **Drop to a terminal.** Something odd happens; I open the web terminal attached to that agent's tmux session, or SSH in from Termius and `tmux attach` to the same session.

## Scope and key decisions

The dashboard is a new standalone project: a Go backend and a React PWA shipped as one binary, running agents in Docker by default behind a pluggable runtime interface.

| Decision | Choice | Why |
| --- | --- | --- |
| Project | agent-dashboard: standalone and personal (not open source), separate from Yggdrasil | Different target (Linux server, phone-first) and lifecycle |
| Backend | Go, single binary, SQLite | My default stack; one process to deploy and back up |
| Frontend | React + TypeScript, installable PWA, embedded in the binary | Mobile-first, web push, rich terminal/diff components |
| Users | Single user | Personal tool; no RBAC complexity |
| Network | Tailscale-only by default; no public port | The app can execute code, so it must not face the internet |
| Agent runtime | Pluggable; `docker` default, `host` (tmux) opt-in | Isolation from the host and other services; host mode for trusted, interactive work; Arcana later |
| Session layer | tmux inside every runtime | Same session reachable from the UI, web terminal and plain SSH |
| Parallelism | One git worktree per agent | Agents never share a working tree |
| State machines | Agent and task lifecycles built with Model Functional Effect | Explicit states and transitions, testable without IO |
| Deployment | Binary runs directly under systemd as user agentd, not inside Docker | Saves RAM on a 2 GB box; direct access to host tmux and the Docker daemon; simple upgrades |
| GitHub access | One fine-grained token on selected repos (Contents and Pull requests: read/write), held by the dashboard | Covers clone, push, PRs and repo import with one credential; agents never receive it |
| Concurrency | 1 running agent by default, max 2; the rest queue | 2 vCPU / 2 GB fits one agent with builds, or two light ones |
| Repos that need docker compose | Set per repo; those repos use the host runtime | Docker-in-Docker is heavy and awkward, especially on 2 GB |

### Runtime trade-off

| Runtime | Isolation | Cost | Best for |
| --- | --- | --- | --- |
| `docker` (default) | Only the worktree mounted; CPU/RAM limits; network rules; disposable | More RAM, toolchain images to maintain, credential mounting, awkward Docker-in-Docker | Unattended and auto-approve runs |
| `host` (tmux as a dedicated Linux user) | Linux user permissions only; can reach localhost services | Lightest, full host toolchain, no image upkeep | Interactive work I am watching, repos that need `docker compose` |

### Supported harnesses (v1)

| Harness | Headless run | Auth on a server | Notes |
| --- | --- | --- | --- |
| Claude Code | `claude -p` with streaming JSON output, resumable sessions | Claude subscription via a long-lived token from `claude setup-token`, or an Anthropic API key | First-class: subagents, hooks, permission prompts surfaced in the UI |
| Codex CLI | `codex exec` with JSON output | ChatGPT sign-in or OpenAI API key | Has its own sandbox modes; set them to match the runtime |
| Gemini CLI | `gemini -p` with JSON output | Google sign-in or Gemini API key | Open source (Apache 2.0) |
| Muse Code (Meta) | `muse exec`, `muse resume` | API key via `muse auth set --api-key-stdin` | Beta, proprietary; uses Meta's own protocol; on-request approvals by default |

Exact flags are pinned per harness version in its adapter and checked by a smoke test; CLIs change fast, so the PRD does not freeze them.

## Functional requirements

Priority: **P0** = v1 must-have, **P1** = v1 if time allows, **P2** = later.

### Repos

- **P0** Add a repo by GitHub URL; clone into `~/workspace/<repo>`. Clones over HTTPS with the dashboard's fine-grained GitHub token.
- **P0** Repo list: name, default branch, current branch, ahead/behind, dirty state, active agents, last activity.
- **P0** Fetch / pull on demand; optional scheduled fetch.
- **P0** Per-repo settings: default harness, runtime, container image, setup command (e.g. `npm ci`), env vars (non-secret), allowed tools.
- **P1** Read `AGENTS.md` / `CLAUDE.md` and show it; edit it in the UI.
- **P1** Import from my GitHub repo list using the same fine-grained token.
- **P2** Repo groups (e.g. "cheSKP", "portfolio") for agents that span several repos.

### Agents

- **P0** Start an agent: pick repo, harness, model, base branch, new branch name (default `agent/<slug>`), runtime, and a prompt. The dashboard creates the worktree, starts the runtime, launches the harness inside tmux.
- **P0** Two modes: **interactive** (chat-style, I send follow-ups) and **task** (one prompt, runs to completion unattended, then stops).
- **P0** Live view: streamed messages, tool calls, current file, token/cost if the harness reports it, elapsed time.
- **P0** Controls: send message, interrupt, stop, restart, resume a finished session, delete (worktree + container).
- **P0** Permission prompts surfaced as cards (command, file, diff) with Approve / Deny / Always allow for this agent.
- **P0** Agents survive dashboard restarts and SSH drops; on restart the dashboard re-attaches to running tmux sessions and containers.
- **P0** Concurrency cap (global and per repo) with a queue; queued tasks start when a slot frees. Default 1 running agent, max 2 on the current VPS.
- **P1** Templates: saved prompts + settings ("write tests", "review PR", "upgrade deps").
- **P1** Subagents: show Claude Code subagent activity nested under its parent; manage `.claude/agents/*.md` per repo.
- **P1** Scheduled tasks (cron-style): e.g. nightly dependency check that opens a PR.
- **P2** Multi-repo agent: one agent with several repos mounted.
- **P2** Agent-to-agent handoff (e.g. reviewer agent runs on the coder agent's branch).

### Git operations

- **P0** Per worktree: status, file tree of changes, side-by-side and unified diff (mobile: unified only).
- **P0** Stage / unstage files or hunks, commit with message (with an "ask the agent to write it" button), amend.
- **P0** Branches: create, switch, delete; push with upstream; pull/rebase onto base.
- **P0** Open a PR on GitHub with title and body (body drafted from the agent's summary).
- **P0** Guardrails: no force-push to protected branches; never push to `main` unless explicitly confirmed.
- **P1** Commit log and per-commit diff; revert a commit.
- **P1** Conflict view with "resolve with agent".
- **P2** PR status and CI checks pulled from GitHub.

### Notifications

- **P0** Web Push to the installed PWA for: needs approval, finished, failed, idle too long.
- **P0** Notification links deep-link to the agent's card.
- **P1** Telegram or ntfy as an alternative channel; quiet hours.

### Terminal and system

- **P0** Web terminal (xterm.js) attached to any agent's tmux session or a plain host shell.
- **P0** System panel: CPU, RAM, disk, running containers, per-agent resource use.
- **P1** Harness manager: installed versions, update button, login/auth status per harness.

### Mobile experience

- **P0** Phone-first layout: a home screen of agent cards sorted by "needs me" first, then running, then done.
- **P0** One-handed actions: approve, deny, send message, stop, view diff, commit, push.
- **P0** Usable on a 380 px wide screen; terminal with an extra key row (Esc, Tab, Ctrl, arrows).

## Architecture

One Go binary serves the React PWA and the API, and an orchestrator drives each agent as tmux + harness CLI + worktree inside a runtime; nothing about an agent lives only in the dashboard's memory.

&#91;embedded content: architecture · clients, dashboard binary, runtimes\]

The orchestrator is the functional core: `(state, event) → (state, commands)`; adapters, runtime drivers, git and the notifier are the shell that executes commands. Because every agent runs in tmux, I can also reach it with plain SSH + `tmux attach`, bypassing the dashboard.

### Two extension points

```go
// One per CLI: Claude Code, Codex, Gemini, Muse.
type Harness interface {
    Name() string
    Check(ctx context.Context) (Version, AuthStatus, error)
    StartCommand(spec AgentSpec) []string   // command run inside tmux
    ResumeCommand(spec AgentSpec, sessionID string) []string
    ParseEvents(r io.Reader) <-chan Event      // JSON stream → normalized events
    Respond(a Approval) []byte                 // how to answer a permission prompt
}

// One per isolation strategy: docker, host; Arcana later.
type Runtime interface {
    Create(ctx context.Context, spec RuntimeSpec) (Handle, error) // worktree, mounts, limits, secrets
    Exec(ctx context.Context, h Handle, cmd []string) (Session, error) // inside tmux
    Attach(ctx context.Context, h Handle) (PTY, error)              // web terminal
    Stats(ctx context.Context, h Handle) (Usage, error)
    Destroy(ctx context.Context, h Handle) error
}
```

**Normalized events** every adapter emits: `message`, `tool_call`, `tool_result`, `approval_needed`, `subagent_started`, `subagent_finished`, `usage`, `done`, `error`. The UI only knows these; a harness whose stream cannot be parsed degrades to the raw terminal view.

### Agent lifecycle

`queued → provisioning → running ⇄ awaiting_approval → idle (interactive) → stopped | done | failed`. Every transition is persisted before its side effects run, so a crash leaves a recoverable state.

### Data model (SQLite)

| Table | Key fields |
| --- | --- |
| `repos` | id, name, remote\_url, path, default\_branch, settings\_json |
| `agents` | id, repo\_id, harness, model, runtime, mode, branch, worktree\_path, tmux\_session, container\_id, harness\_session\_id, state, created\_at |
| `events` | id, agent\_id, seq, type, payload\_json, created\_at |
| `approvals` | id, agent\_id, request\_json, decision, decided\_at |
| `templates` | id, name, harness, prompt, settings\_json |
| `schedules` | id, template\_id, repo\_id, cron, enabled |
| `secrets` | id, kind, name, ciphertext |
| `audit_log` | id, actor, action, target, result, created\_at |

### API sketch

| Method | Path | Purpose |
| --- | --- | --- |
| GET / POST | `/api/repos` | List, add (clone) |
| POST | `/api/repos/{id}/fetch` | Fetch / pull |
| GET / POST | `/api/agents` | List, start |
| POST | `/api/agents/{id}/messages` | Send follow-up |
| POST | `/api/agents/{id}/approvals/{aid}` | Approve / deny |
| POST | `/api/agents/{id}/stop`, `/resume` | Lifecycle |
| GET | `/api/agents/{id}/git/status`, `/diff` | Git read |
| POST | `/api/agents/{id}/git/commit`, `/push`, `/pr` | Git write |
| WS | `/ws/events` | Live event stream for all agents |
| WS | `/ws/terminal/{agentId}` | PTY for the web terminal |

## Security and non-functional requirements

The dashboard is a remote code-execution surface on a box that may also run other services, so security requirements are P0 and override convenience.

### Security

- **Network:** binds to the Tailscale interface (or `127.0.0.1` behind Tailscale Serve) only; refuses to start on `0.0.0.0` unless a flag is set. Public SSH is disabled once Tailscale works.
- **App auth:** passkey (WebAuthn) as primary, password + TOTP as fallback; short-lived sessions, CSRF protection, rate-limited login.
- **Process user:** the dashboard runs as a dedicated `agentd` user, not root; it is in the `docker` group (documented as root-equivalent, which is another reason for Tailscale-only).
- **Containers:** non-root user inside, only the agent's worktree mounted read-write, no Docker socket, CPU and memory limits, dropped capabilities; outbound network allowed (agents need APIs and package registries), no access to host ports.
- **Secrets:** harness credentials and the GitHub token encrypted at rest (age or a key derived from a master passphrase); injected per container at start, never written into worktrees or logs; log output scrubbed for known token patterns.
- **Git safety:** agents get no GitHub credentials; the dashboard pushes and opens PRs with its token; protected branch list enforced by the dashboard; GitHub branch protection on `main` recommended in setup docs.
- **Approvals:** auto-approve (`--dangerously-skip-permissions` and equivalents) allowed only in the `docker` runtime, and off by default.
- **Audit log:** every agent start, approval, git push and terminal session recorded with time and outcome.
- **Terms:** setup docs remind me to keep subscription logins within each provider's terms; API keys are the fallback.

### Non-functional

| Area | Requirement |
| --- | --- |
| Footprint | Dashboard under 100 MB RAM idle; each agent container capped at about 700 MB; a 2–4 GB swap file on the VPS |
| Concurrency | Default cap of 1 running agent (max 2) on 2 vCPU / 2 GB, the rest queued; raise it when the VPS grows |
| Resilience | Agents keep running if the dashboard crashes or restarts; dashboard re-attaches on boot (systemd unit with restart) |
| Latency | Agent output reaches the phone within 1 s over Tailscale |
| Storage | Transcripts and logs rotated; worktrees of deleted agents removed; disk-usage warning at 80% |
| Backup | One SQLite file + config + encrypted secrets; documented backup command |
| Install | One install script plus a systemd unit; works on Ubuntu 24.04 |
| Observability | Structured logs; `/healthz`; per-agent resource metrics |

## Milestones

v1 is five phases; I can use the dashboard daily from the end of Phase 1, and each phase closes on a gate I test from my phone.

&#91;embedded content: roadmap · 5 phases, 4 gates\]

Until Phase 3 lands, agents run on the host runtime with permission prompts on (no auto-approve). The orchestrator's state machines are designed and property-tested first in each phase, per Model Functional Effect. A PRD.md, AGENTS.md and PLAN.md in the repo let Claude Code build each phase from this document.

## Decisions and risks

### Resolved questions

| Question | Decision |
| --- | --- |
| Name | agent-dashboard |
| VPS size | 2 vCPU, 2 GB RAM: 1 running agent by default, max 2, plus a 2–4 GB swap file |
| Binary in Docker or under systemd | Directly under systemd |
| Repos that need docker compose | Decided per repo; those use the host runtime |
| GitHub access | One fine-grained token, held by the dashboard only |
| Open source | No, personal |
| Default runtime | Docker stays the default; host (tmux) is opt-in per repo |

### Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Harness CLIs change flags and output formats often | Adapters break silently | Pin versions, adapter smoke tests on upgrade, raw-terminal fallback view |
| Subscription login on an always-on server may conflict with provider terms or expire | Agents stop or account issues | Re-check terms, auth-status panel with expiry warnings, API-key fallback per harness |
| Approvals are hard to intercept uniformly across harnesses | Prompts hidden in the terminal | Claude Code: hook or permission-prompt tool; others: detect prompt state from the stream, else notify "needs input" and open the terminal |
| VPS RAM exhaustion with several agents plus builds | Other services slow down or get OOM-killed | Per-container memory limits, cap of 1–2 agents, swap, queue, alerts; move to 4 GB for 3+ agents |
| A compromised dashboard = shell on the VPS | Full host compromise | Tailscale-only, passkeys, non-root, containers, audit log |
| Scope creep toward a full IDE | v1 never ships | Non-goals list; terminal as the escape hatch |

## Sources

- [Getting Started with Muse Code: CLI commands (QAInsights)](https://qainsights.com/?p=18730)
- [Muse Code GUI and protocol notes (Product Compass)](https://www.productcompass.pm/p/muse-code-gui)
- [Claude Code on a VPS (Product Compass)](https://www.productcompass.pm/p/claude-code-vps)
- [xa: managing remote Claude Code sessions (PyPI)](https://pypi.org/project/xa/0.1.3/)
