# Audit remediation results

Branch: `codex/audit-remediation`. Scope: repairs to the implemented product,
authorized on 2026-10-30. Product feature proposals remain backlog work.

## Changes

- Durable, serialized lifecycle transitions and an ordered outbox; restart replay,
  FIFO queue recovery, physical capacity reservations, recurring schedule completion,
  approval ownership and explicit resume/interrupt controls.
- Independent Git checkouts with protected metadata and collision refusal;
  credential-free repository configuration, canonical remote validation and private
  authenticated network operations; protected push checks use actual HEAD.
- Independent encryption key files, migration of notification credentials, bounded
  passkey ceremonies and request bodies, authenticated agent hooks, peer-address
  login throttling, WebSocket origin/session checks and audit redaction.
- Runtime output replay and real exit status, cancellation-aware parsers, bounded
  redacted transcripts, interactive terminal handling and container UID matching.
- Compiled mobile utility styles, bounded sequence-based transcript replay,
  reconnect/deep-link handling, stale-response guards and lazy terminal/Git views.
- Real frontend tests, race-enabled Go tests, purity enforcement, current frontend
  tooling, Go 1.27.3 and a fix for reachable JWT vulnerability GO-2025-3553.

Design and decisions: [remediation design](design/audit-remediation.md),
[durable orchestration](adr/0008-durable-orchestration.md),
[security](adr/0009-security-remediation.md),
[frontend/build tooling](adr/0011-frontend-verification.md).

## Test evidence

Final verification on 2026-10-31 uses Go 1.27.3:

- `make verify`: passed Go vet, golangci-lint (0 issues), frontend lint/type-check,
  purity scan (15 core files, no effects), Go race suite, 27 frontend tests and
  production frontend/embedded Go builds. Linux live contracts remain gated.
- Final npm audit: zero vulnerabilities after pinning minimatch 11.0.1 via an
  override for ESLint/typescript-eslint's transitive dependency. The fresh audit
  found GHSA-g3hv-75wg-m249 in 10.2.6; the override and its rationale are in ADR 0011.
- Shutdown regression: `go test -race ./internal/shell/orchestrator -run
  TestInteractiveHookSessionIdentityIsDurable -count=20` passed (20 repetitions).
- `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`: zero reachable
  vulnerabilities and zero imported-package vulnerabilities; one finding remains
  in an unused required module (OpenPGP).
- After the SQLite upgrade, Linux amd64 test binaries for runtime, host and Docker
  cross-compiled with CGO disabled. These binaries were not executed and were
  removed after compilation.

SQLite was upgraded from modernc.org/sqlite v1.33.1 to v1.61.0, with its required
transitive updates. The old driver's query cancellation path discarded live rows
without closing them, leaving shell.db open during Windows TempDir cleanup.
The new driver's stmt.query closes rows before returning cancellation/errors.
Restart-test instances also now call Shutdown before store cleanup. The repeated
race regression above passes without sleeps or suppressed cleanup failures.

Generated `web/dist` is refreshed by the production build. Scratch directories and
cross-compiled test artifacts were removed; `git diff --check` is clean.

Frontend fault-injection checks caught seven deliberate defects in isolated copies:
unbounded history, wrong-agent updates, invalid timestamps, nonpositive sequences,
reconnect cursor reset, discarded live frames during replay, and stale approval
responses after switching agents. The replay test was strengthened after its first
mutation survived; its repeated mutation then failed as expected.

Eight orchestration mutations and the high-risk security mutation checks are
documented in ADR 0008 and [security validation](design/security-validation.md).
Compiler failures and timeouts are not counted as caught mutations.

## Upgrade and acceptance limits

- Back up both SQLite and its separate `.key` file. Re-enter legacy credentials
  that used the old database-password-hash fallback; they cannot be safely migrated
  automatically. Explicit-passphrase records migrate with their original passphrase.
- Legacy queued/provisioning records lack the prompt/spec required for recovery.
  They fail explicitly for resubmission; their checkouts are preserved. New records
  persist complete specifications.
- Build the sandbox image with the installed `agentd` UID/GID, following the
  installer output and runtime ADR. Docker approval callbacks need a reachable
  Tailscale dashboard origin; container loopback cannot reach the host dashboard.
- Windows unit tests and Linux cross-compilation do not validate a live tmux/Docker
  deployment. Test service restart survival, provider CLI hooks, sandbox isolation,
  phone layouts/passkeys, PWA reconnect and push delivery using the README checklist.
  No deployment or real provider notification is performed by this remediation.
