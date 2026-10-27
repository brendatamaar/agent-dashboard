# ADR 0007: Antigravity CLI (`agy`) Harness Adapter

- **Status:** Accepted
- **Date:** 2026-10-27
- **Context:** Google has migrated Gemini Code Assist for Individuals and Google One AI Premium (Google Pro) developer subscriptions to the Antigravity suite (`https://antigravity.google`). The legacy `@google/gemini-cli` Code Assist OAuth endpoint is deprecated. To enable users with Google Pro / Antigravity subscriptions to use their quotas directly, we introduce a first-class `antigravity` harness (`agy`).

## 1. CLI Details & Semantics

- **Binary:** `agy` (Google Antigravity CLI)
- **Headless Execution:**
  ```bash
  agy -p "<prompt>" --output-format stream-json
  ```
  - Emits newline-delimited JSON (`stream-json`) events on `stdout`.
  - Additional flags: `--model <MODEL>`, `--mode <auto|plan|accept-edits>`.
- **Session Resume:**
  ```bash
  agy -p "<prompt>" --output-format stream-json --conversation <SESSION_ID>
  ```
  - In interactive mode: `agy -c <SESSION_ID>`.
- **Authentication:**
  - Authenticated via Google Pro / Google One AI Premium subscription session stored under `~/.gemini/` or `~/.antigravity/` from `agy` login.
  - Can also use Google Cloud Application Default Credentials (`AGY_ADC_AUTH=true`).
  - Auth check verified via `agy whoami` or inspecting cached session credentials.
- **Approval Behavior:**
  - Supports `--mode <mode>` (`default`, `auto`, `plan`).
  - In Docker sandbox runtime with auto-approve enabled: `--mode auto` or `--yolo`.
- **Subagents:**
  - Antigravity supports native multi-agent and subagent delegation.
  - Subagent events emit lifecycle payloads in the `stream-json` feed mapped to `subagent_started` and `subagent_finished`.

## 2. Normalization Strategy

The `antigravity` adapter streams `events.NormalizedEvent` payloads (`message`, `tool_call`, `tool_result`, `subagent_started`, `subagent_finished`, `usage`, `done`, `error`) and gracefully degrades to raw tmux PTY terminal if unparseable output occurs.
