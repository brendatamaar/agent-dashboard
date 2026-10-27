# ADR 0005: Docker Runtime Isolation, Secret Injection, and Resource Management

- **Status:** Proposed
- **Date:** 2026-10-27
- **Context:** Phase 3 requires introducing the `docker` runtime as the default isolation layer for agents on a small Linux VPS (2 vCPU, 2 GB RAM). Agents must run isolated from the host and each other, with strict memory limits (~700 MB each), dropped capabilities, secret injection without leaking to disk or worktrees, hook access for approval interception, and explicit OOM detection.

## 1. Container Isolation & Permissions

- **Image:** A lightweight base (Debian/Ubuntu-slim or Alpine-based with glibc) containing `git`, `tmux`, `curl`, `jq`, Node.js (for Claude Code / harness CLIs), and the harness binaries.
- **User Permissions:** 
  - Containers run as non-root user `agentd` (UID 1001 / matching host `agentd`).
  - Worktrees are bind-mounted at `/workspace`. Because UID matches, file creations and git commits retain correct ownership on host filesystem without permission conflicts.
- **Hardening:**
  - Security options: `--cap-drop=ALL`, `--security-opt=no-new-privileges`.
  - No host ports exposed (`-p` is never used).
  - No access to `/var/run/docker.sock` inside agent containers (repos requiring Docker Compose must be configured to use the `host` runtime).
  - Only the agent's designated worktree (`~/workspace/<repo>/agent/<slug>`) is mounted read-write.

## 2. Network & Approval Hook Interception

- **Problem:** Dashboard binds to Tailscale (`tailscale0`) or `127.0.0.1`. Agent containers need outbound internet for LLM APIs (Anthropic / OpenAI / etc.) and must reach the dashboard's approval hook endpoint (`POST /api/internal/hooks/approval`).
- **Decision:**
  - Containers run on Docker's default bridge network (outbound WAN access enabled for provider APIs).
  - To reach the dashboard's approval hook from the container:
    - On Linux, Docker bridge gateway IP is `172.17.0.1` (or via `--add-host=host.docker.internal:host-gateway`).
    - The dashboard HTTP listener adds an optional internal bind to the Docker bridge gateway interface or loopback reverse proxy, or allows internal hook requests from `172.17.0.0/16`.
    - Each container receives an environment variable `DASHBOARD_URL=http://host.docker.internal:<port>` and a single-use or agent-scoped bearer token `AGENTD_HOOK_SECRET` for authenticating hook callbacks.

## 3. Secret Injection & Leak Prevention

- **Problem:** Credentials (e.g. Anthropic API key, Claude setup token) must be provided to the harness inside the container, but must never be written to the worktree git repo, container image, command-line arguments (visible in `ps`), or persisted in logs.
- **Decision:**
  - Secrets are retrieved decrypted in-memory by the orchestrator at start time.
  - Injected strictly as container environment variables during `docker create` / `docker run -e CLAUDE_CODE_TOKEN=... -e ANTHROPIC_API_KEY=...`.
  - Scrubbing rules (`internal/core/scrub`) sanitize all event streams, terminal captures, and logs to ensure tokens matching `sk-ant-` and other patterns are redacted before persistence or broadcast.

## 4. Resource Limits & OOM Detection

- **Limits:**
  - Memory: `--memory=700m --memory-swap=1200m` per container.
  - CPU: `--cpus=1.0` quota to prevent a runaway agent from starving the host.
- **OOM Detection:**
  - Docker inspect tracks `State.OOMKilled` (boolean) and `State.ExitCode == 137`.
  - When a container exits with code 137 or `OOMKilled == true`, the agent transitions to `StateFailed` with an explicit reason `oom_killed` (rather than a generic crash).
  - The UI displays an OOM alert badge with recommendations (e.g., "Killed by OOM. Memory exceeded 700 MB").

## 5. Concurrency & Queue Policy

- Global running cap: 1 by default, configurable to max 2.
- Per-repo running cap: default 1 (preventing concurrent conflicting git locks or parallel CPU contention on the same repo).
- Additional tasks queue in FIFO order with state `queued`. When an agent terminates (`done`, `stopped`, `failed`), the next eligible task in queue is automatically dequeued and provisioned.
