# ADR 0006: Harness Adapters for Codex CLI, Gemini CLI, and Muse Code

- **Status:** Proposed
- **Date:** 2026-10-27
- **Context:** Phase 4 Spike (`PLAN.md`) requires inspecting `--help` and runtime behaviors for Codex CLI, Gemini CLI, and Meta's Muse Code to record their headless execution commands, session resume strategies, authentication methods, and approval handling.

## 1. Codex CLI (OpenAI)

- **Detected Version:** `codex-cli 0.140.0` (standalone binary)
- **Headless Execution:**
  ```bash
  codex exec [PROMPT] --json
  ```
  - Emits JSONL events directly to `stdout`.
  - Additional flags: `-m, --model <MODEL>`, `-C, --cd <DIR>`.
- **Session Resume:**
  ```bash
  codex exec resume [SESSION_ID] [PROMPT] --json
  ```
  - Interactive resume: `codex resume [SESSION_ID]`.
  - Flags support `--last` to pick the newest session.
- **Authentication:**
  - ChatGPT tokens stored in `~/.codex/auth.json` or `OPENAI_API_KEY` injected as an environment variable.
  - Health and auth check via `codex doctor --json`.
- **Approval Behavior:**
  - Configurable via `-a, --ask-for-approval [untrusted|on-request|never]`.
  - In Docker runtime, `--dangerously-bypass-approvals-and-sandbox` can be toggled if auto-approve is enabled for the agent.

## 2. Gemini CLI (Google)

- **Package & Binary:** `@google/gemini-cli` (`gemini`), Apache-2.0.
- **Headless Execution:**
  ```bash
  gemini -p "<prompt>" --output-format stream-json
  ```
  - Emits newline-delimited JSON events on stdout.
- **Session Resume:**
  ```bash
  gemini -p "<follow-up>" --output-format stream-json --resume <session-id>
  ```
  - Interactive mode inside tmux: `gemini --resume <session-id>`.
- **Authentication:**
  - `GEMINI_API_KEY` environment variable or Google OAuth credentials.
  - Status verified via `gemini auth status --json` or checking `GEMINI_API_KEY`.
- **Approval Behavior:**
  - In `-p` headless runs, mutating actions require flags or are gated; in interactive mode, prompts render in TUI with fallback to web terminal.

## 3. Muse Code (Meta)

- **Detected Version:** `Muse Code 1.4.11 (1.4.11-R6667.1)`
- **Headless Execution:**
  ```bash
  muse exec [PROMPT] --json --workspace <PATH>
  ```
  - Emits machine-readable JSONL events on stdout.
  - Supports explicit session id reservation via `--session-id <UUID>`.
- **Session Resume:**
  ```bash
  muse resume <session-ref>
  ```
  - In headless mode, resume is also executed via `muse exec` targeting the existing workspace or session.
- **Authentication:**
  - `MUSE_API_KEY` / `META_API_KEY` environment variable or stdin setup via `muse auth set --api-key-stdin`.
- **Approval Behavior:**
  - Supports `--approval-mode [untrusted|on-request|never]`.
  - In Docker runtime with auto-approve enabled: `--yolo` or `--disable-approval`.

## 4. Normalization Strategy

All three harnesses parse their JSONL / stream-json output into the shared `events.NormalizedEvent` stream (`message`, `tool_call`, `tool_result`, `approval_needed`, `usage`, `done`, `error`, `subagent_started`, `subagent_finished`). In interactive mode or when output parsing encounters unknown structures, the dashboard seamlessly degrades to the raw tmux terminal view.
