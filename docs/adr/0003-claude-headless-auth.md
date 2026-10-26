# ADR 0003: Claude Code Headless Authentication on VPS

- **Status:** Accepted
- **Date:** 2026-10-26
- **Context:** Phase 1 Spike (`PLAN.md`) requires establishing how Claude Code authenticates on a headless server (Ubuntu VPS under user `agentd`) using either a Claude subscription or an Anthropic API key, where credentials live, and compliance considerations.

## 1. Authentication Mechanisms

Claude Code CLI supports two authentication paths:

1. **Anthropic API Key:**
   - Environment variable: `ANTHROPIC_API_KEY`.
   - Simple and standard for server environments; pay-per-token API usage.

2. **Claude Subscription (Pro / Team / Max):**
   - Headless token setup: Run `claude setup-token` on a desktop machine with an active browser session. This generates a long-lived OAuth token.
   - Alternatively, copy the generated credentials file from `~/.claude.json` / `~/.claude/` to `/home/agentd/.claude/` with permissions restricted to `agentd:agentd` (`0600`).
   - The token can also be injected via the `ANTHROPIC_AUTH_TOKEN` or `CLAUDE_CODE_OAUTH_TOKEN` environment variable into tmux sessions spawned for the agent.

## 2. Credentials Storage & Security in agent-dashboard

- Credentials (API key or subscription token) are stored encrypted in the dashboard's SQLite `secrets` table (`kind = 'harness_auth'`, `name = 'claude_token'` or `'anthropic_api_key'`).
- The dashboard decrypts the secret in memory when launching an agent and passes it in the environment of the spawned tmux process.
- Credentials are never written to disk unencrypted, never logged to `audit_log`, and scrubbed from all event streams and log outputs using `internal/core/scrub`.

## 3. Provider Terms Reminder

> **Note:** When using a Claude Pro/Team subscription on a server or headless automated environment, verify Anthropic's current Terms of Service and usage policies regarding automated and programmatic access. For unattended workloads, an API key with usage limits is recommended.
