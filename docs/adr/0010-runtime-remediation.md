# ADR 0010: Recoverable runtime output and credential-isolated Git operations

- Status: Accepted (authorized audit remediation)
- Date: 2026-10-30

The original Docker command detached tmux as PID1, so the container exited when
that command returned. Runtime Exec only sent input; it never supplied output.
Missing sessions and inspection errors were reported as successful exit zero.
Linked worktree metadata pointed outside the single permitted mount, and clone
configuration could retain the dashboard GitHub credential.

Runtime now separates Output (full durable replay followed by live bytes), Exec
(one literal input payload plus its terminating Enter) and Interrupt (an actual
control-C). Inspection returns ErrNotFound for an absent session and an error
for unknown outcomes. A Python3 supervisor owns tmux startup, waits for recorded
process completion and preserves the actual exit code. The Docker supervisor
stays in the foreground as PID1. Host sessions use an explicit per-run tmux -S socket and
private per-run files under the configured dashboard data directory, outside
all worktrees. The socket is stored under that durable run directory rather
than /tmp, so systemd PrivateTmp namespaces cannot hide it after a restart. SSH
attachment uses tmux -S /var/lib/agent-dashboard/runtime/<run>/tmux.sock attach.
Closing Output or shutting down the dashboard stops only the
reader process. Explicit Destroy removes the runtime and its private artifacts.

Task stdout/stderr is scrubbed before any disk write, including selected
credential values and known provider token forms; streaming retains potential
split secrets until they can be safely redacted. A run fails with exit 125 at a
64 MiB transcript cap instead of truncating a replay source. Interactive harnesses
inherit the tmux terminal directly, retaining a real TTY. Their terminal bytes
are not captured to disk; the web terminal is their explicit fallback. Structured
interactive events remain unavailable apart from authenticated hook callbacks.
TTY resize now changes the underlying PTY dimensions.

Python3 is an explicit dependency of host mode and the sandbox image. The image
installs pinned Claude Code, Codex and Gemini packages and verifies their version
commands at build time. Muse/Antigravity require operator-supplied images. Local
CLI help checked on 2026-10-30: Claude 2.1.314, Codex 0.140.0 and Gemini 0.65.0;
the default image pins registry-verified Claude 2.1.314, Codex 0.166.0 and Gemini 0.66.0.
A provider-authenticated image smoke test is still required before deployment.

The Docker image account must match the existing dedicated host agentd account.
The runtime refuses root and checks ownership labels before launch. Build from
the scripts/docker context with the actual service IDs:

```sh
docker build --build-arg AGENTD_UID="$(id -u agentd)" \
  --build-arg AGENTD_GID="$(id -g agentd)" \
  -t agentd/sandbox:latest scripts/docker
```

Custom images must retain the supervisor and matching agent-dashboard.uid/gid
labels, account and home ownership. No host UID changes or broad permission
changes to worktrees are required.

For sandbox isolation, an agent worktree is now an independent, no-hardlink Git
checkout with its own .git directory. This deliberately refines the PRD's linked
worktree implementation: mounting dashboard-owned shared metadata would expose
other repositories, credentials and refs. It costs additional object disk space,
while retaining separate branches, base commits, path-based Git operations and
explicit cleanup. The dashboard parent checkout and agent checkout do not share
mutable refs. Push/fetch use the selected checkout, and repository overview still
uses the dashboard checkout. Existing paths are refused; a managed ownership
marker allows reconciliation of an already completed creation. Cleanup is
confined to the configured workspace and refuses ancestor symlink escapes.

GitHub credentials exist only in transient environment configuration, scoped to
the immutable canonical GitHub URL. Before a credential is provided, the service
checks the persisted URL against origin and refuses agent-defined network
configuration. The credential-bearing process runs in a separate private bare
shadow repository with dashboard-written config, so racing agent config edits
cannot add helpers, rewrites, proxies or redirects. Token-free local Git syncs
refs/objects afterward. Dashboard Git commands receive a basic environment
allowlist and disable hooks, preventing project hooks from inheriting the master
key. The old persisted http.extraHeader is removed without reading or reporting
its value before network operations or agent checkout creation. Changed-file
paths use NUL-delimited porcelain rather than quoted newline records. The PR
regression invokes the actual service through an injected HTTP endpoint.

Claude is launched with an explicit PreToolUse settings entry for the private
Python helper. The helper posts a scoped bearer token and stable UUID to the
approval endpoint, retries transient restart failures under the same UUID for
up to 300 seconds, and returns the documented allow/deny decision JSON. Missing,
expired, malformed or unreachable approval responses fail closed. Auto-approve
mode skips this hook and remains Docker-only. Allowed-tool lists are applied by
Claude and rejected for adapters without that capability. Hook settings follow
[Claude's official reference](https://code.claude.com/docs/en/hooks).
Docker callbacks require a dashboard origin reachable from the container,
normally its configured Tailscale HTTPS origin. A loopback origin explicitly
denies; no public listener, host network mode or extra host mount is introduced.

Validation map (Windows tests run locally; Linux/live gates reported separately):

| Contract or boundary | Regression |
| --- | --- |
| Existing checkout never deleted; independent metadata; owned reconciliation | TestWorktreeNeverDeletesExistingData, TestWorktreeOwnMetadataAndSafeConfig |
| Immutable origin, no credential argv/config, race-safe shadow | TestNetworkRejectsMutableOriginsAndCredentialConfig, TestCredentialIsTransientAndOriginScoped, TestNetworkUsesTrustedShadowDuringConfigRace |
| Legacy auth removal and service environment isolation | TestLegacyCloneCredentialMigration, TestGitCommandsDoNotInheritServiceSecrets, TestAgentEnvironmentDoesNotInheritDashboardSecrets |
| Exact path bytes | TestPorcelainPreservesPathBytes |
| Actual PR service HTTP request | TestCreateGitHubPR |
| Output replay, literal input, exit code and missing sessions | shared runtime contract, TestFakeRuntimeReplayExitAndLiteralInput, TestLiveDurableOutputInputAndExit |
| Redaction before disk, bounded transcripts, cancellation | TestSupervisorRedactsBeforeDiskAndPreservesExit, TestSupervisorTranscriptLimitFailsWithoutTruncation, TestOutputReaderCancellationUnblocksRead |
| Hook request/auth/UUID retry and failed-closed transport | TestApprovalHookBridgeAllowsDeniesAndRetriesStableID, TestHookSettingsUseQuotedPrivateHelper |
| Real interactive TTY and resize | TestInteractiveSupervisorPreservesTTYWithoutCapture, TestPTYResizeUsesActualTerminal (Linux) |
| Parser cancellation/backpressure/fallback and Claude failures | TestParserCancellationAndFallbackContract, TestClaudeFailureResultIsError |
| Unsupported production platform | TestWindowsRuntimeIsExplicitlyUnsupported |

Gemini modern schema regressions are synthetic examples derived from the
[official stream types](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/output/types.ts); provider-recorded compatibility remains a live gate.

Run Linux live contracts as the non-root service user with tmux, Python3, Docker
access and the matching prebuilt image: AGENTD_RUNTIME_INTEGRATION=1 go test
./internal/runtime/.... No real provider requests or credentials are required for
those contracts. Provider CLI fixture/flag compatibility and a phone terminal
check remain deployment gates; fake adapters do not establish those properties.

Final focused verification on 2026-10-31 passed `go test ./internal/runtime/...
./internal/harness/... ./internal/shell/git/...` on Windows. Linux amd64 test
binaries cross-compiled successfully for runtime, host and Docker packages; they
were not executed on this Windows machine. The embedded and Docker supervisor
copies have identical SHA256 hashes.

Thirteen deliberate scratch-copy mutations were caught: existing-path refusal,
credential URL scope, trusted-config race isolation, exact path bytes, legacy
credential cleanup, parser cancellation, Claude failure results, custom-secret
redaction, transcript cap, real exit status, hook fail-closed behavior, Gemini
content fields, and nonblocking parser-error fallback. Scratch copies and test
binaries were removed after recording this evidence.
