# ADR 0002: Claude Code Event Sources and Session Resume

- **Status:** Accepted
- **Date:** 2026-10-26
- **Context:** Phase 1 Spike (`PLAN.md`) requires determining the event stream mechanism and session resume strategy for Claude Code (verified against Claude Code CLI v2.1.299).

## 1. Task Mode (Non-interactive)

### Command Execution
```bash
claude -p --output-format=stream-json --verbose <prompt> < /dev/null
```

### Critical Findings & Requirements
- In `-p` / `--print` mode, `--output-format=stream-json` **strictly requires** `--verbose`. Omitting `--verbose` causes Claude Code to immediately fail with `Error: When using --print, --output-format=stream-json requires --verbose`.
- Claude Code checks `stdin` on start; if `stdin` is left open without input, it waits 3 seconds before continuing. Redirecting `stdin` (`< /dev/null` or closing stdin) avoids this delay.
- The output on `stdout` is newline-delimited JSON (NDJSON) containing structured events:
  - `{"type":"system", "subtype":"init", "session_id":"...", "tools":[...], "model":"..."}`
  - `{"type":"assistant", "message":{"content":[{"type":"thinking", ...}, {"type":"text", ...}, {"type":"tool_use", ...}]}}`
  - `{"type":"user", "message":{"content":[{"type":"tool_result", ...}]}}`
  - `{"type":"result", "subtype":"success", "result":"...", "usage":{...}, "total_cost_usd":...}`
- The dashboard parser maps these events into normalized internal events:
  - `message` (assistant text, user prompts)
  - `tool_call` (tool invocation name, arguments)
  - `tool_result` (command output, file contents)
  - `usage` (token usage, cost USD)
  - `done` (completed with terminal_reason)
  - `error` (failure)

## 2. Interactive Mode

### Command Execution
Interactive mode runs inside a dedicated `tmux` session on the host (e.g. `tmux new-session -d -s agent-<id> -c <worktree_path> "claude"`).

### Event Source & Fallback
1. **Raw Terminal Fallback:** Web terminal (`xterm.js`) attaches directly to the tmux session via a PTY bridge (`internal/runtime/host`). If structured events fail or are unavailable, the raw terminal view provides 100% fidelity.
2. **Transcript Tailing:** Claude Code writes persistent session transcripts in JSONL format to `~/.claude/projects/<project-slug>/<session-id>.jsonl`. The dashboard adapter tails this file to extract live messages, tool calls, and status updates for the phone UI card without needing complex terminal emulation scrapers.
3. **Session ID Discovery:** The session ID is obtained either from the initial command output/environment or by discovering the latest `.jsonl` file created in `~/.claude/projects/<project-slug>/`.

## 3. Session Resume

- Resuming an existing session is done using:
  ```bash
  claude --resume <session-id>
  ```
  or in task mode:
  ```bash
  claude -p --output-format=stream-json --verbose --resume <session-id> <follow-up-prompt> < /dev/null
  ```
- Resuming maintains full context and message history across agent restarts.
- Resuming a completed or interrupted session attaches to the same transcript, preserving continuity.
