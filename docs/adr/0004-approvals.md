# ADR 0004: Approval Interception Mechanism and Fallback Strategy

- **Status:** Proposed
- **Date:** 2026-10-26
- **Context:** Phase 2 Spike (`PLAN.md`) requires comparing a `PreToolUse`-style hook against the headless permission-prompt option for surfacing Claude Code permission prompts to the user's phone, and defining a fallback mechanism.

## 1. Evaluation of Approaches

### Option A: `PreToolUse` Hook with Dashboard Callback (Chosen Primary)
- **Mechanism:** Claude Code supports lifecycle hooks configured in `.claude/settings.json` within the agent's worktree. Specifically, `PreToolUse` fires synchronously before any tool (`Bash`, `FileEdit`, `FileWrite`, etc.) runs.
- **Workflow:**
  1. A lightweight hook script (`agentd-hook`) or curl invocation is configured in the agent's worktree `.claude/settings.json`.
  2. When Claude Code attempts a potentially sensitive tool use, the hook receives the tool name and input JSON via `stdin`.
  3. The hook makes a blocking HTTP POST to the local dashboard endpoint:
     `POST http://127.0.0.1:<port>/api/internal/hooks/approval` with `agent_id`, `tool_name`, and `tool_input`.
  4. The dashboard records the approval in SQLite, transitions agent state to `awaiting_approval`, pushes a Web Push notification to the phone, and broadcasts a WebSocket event.
  5. The hook connection blocks waiting for decision.
  6. On the phone, the user taps "Approve", "Deny", or "Always Allow":
     - **Approve:** Dashboard responds HTTP 200 `{"action":"allow"}`; hook exits 0. Claude executes the tool.
     - **Deny:** Dashboard responds HTTP 200 `{"action":"deny"}`; hook exits non-zero (or outputs deny JSON). Claude informs the model of the denial.
     - **Always Allow:** Dashboard persists the rule in-memory/DB for this session, then unblocks with allow. Subsequent matching tool calls are immediately allowed without waking the user.
- **Advantages:**
  - Pristine structured data (exact command line, diff, file path) without ANSI terminal scraping.
  - Works reliably across both interactive tmux sessions and task mode.
  - Independent of terminal dimensions or layout.

### Option B: Headless Permission Prompts (`--permission-prompts host`)
- **Mechanism:** In `--print` / SDK mode, Claude Code supports `--permission-prompts host`, emitting permission requests over stdout.
- **Limitations:**
  - Interactive mode inside tmux uses an Ink TUI with interactive curses-style keyboard prompts (`[y/n/a]`). Terminal scraping of TUI prompts is brittle and susceptible to race conditions and ANSI escape artifacts.
  - Does not provide clean structured diffs or arguments when running inside tmux.

## 2. Decision

Adopt **Option A (`PreToolUse` hook callback)** as the primary interception mechanism for structured approval cards.

## 3. Fallback Strategy

If a harness prompt cannot be intercepted via hooks (e.g. interactive git credential prompt, non-hook harness, or unexpected CLI query):
1. The dashboard monitors agent transcript / terminal activity. When detected in an awaiting/idle state inside tmux with no hook pending, the agent state transitions to `idle` or `awaiting_approval`.
2. A Web Push notification is dispatched: `"Agent needs input"`.
3. Tapping the notification opens the agent card in the phone UI and automatically focuses the embedded **web terminal (xterm.js)** with the mobile extra-key row (Esc, Tab, Ctrl, Enter, Arrows), allowing the user to view and respond to the live terminal directly in two taps.
