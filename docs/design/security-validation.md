# Security remediation verification

Service profile. The shared audit design is `audit-remediation.md`; this is its
security boundary test map and focused verification record.

| Requirement / invariant | Regression test | Result |
| --- | --- | --- |
| Peer identity cannot be forged with forwarding headers or source ports | `TestPeerIPThrottlingCannotBeForged` | passing |
| Passkey challenges are bounded, expire, bind identity/ceremony, consume once | `TestPasskeyChallengesBoundExpireAndBindIdentity` | passing |
| Internal approval requests require per-agent bearer credentials | `TestHookRequiresScopedCredentials` | passing |
| API bodies are bounded | `TestAPIBodyLimit` | passing |
| WebSockets reject unrelated origins and close on revocation/shutdown | `TestWebSocketOriginRevocationAndShutdown` | passing |
| General terminal uses host runtime and persists an audit entry | `TestTerminalSelectsHostAndAudits` | passing |
| Event history query cursors/limits are validated | `TestEventHistoryQueryBounds` | passing |
| Malformed staging cannot stage files; pushes guard actual HEAD | `TestMalformedStageAndPushUseActualHead` | passing |
| Encryption key is independent of SQLite/login credentials; missing key fails closed | `TestVaultIndependentKeyAndRestart` | passing |
| Legacy migration preserves ciphertext on failure and requires explicit passphrase | `TestVaultLegacyMigrationPreservesDataAndRequiresExplicitPassphrase` | passing |
| Error/Any/nested/bound attributes share dynamic log redaction | `TestRecursiveAnyErrorAndDynamicSecrets` | passing |
| Notification secrets migrate encrypted without rotating VAPID identity; failed saves retain effective settings | `TestNotifierEncryptsAndMigratesExistingChannelSecrets` | passing |
| Quiet hours receive localized time from shell | `TestNotifierLocalizesQuietHoursBeforePurePolicy` | passing |
| Caller mutation cannot alter default branch security | `TestDefaultPushPolicyCannotMutateGlobalGuards` | passing |
| Subagent files round trip and cannot follow symlink directories | `TestSubagentRegularFileRoundTrip`, `TestSubagentFilesystemCannotEscapeThroughSymlinks` | round trip passing; symlink case skipped on Windows without symlink privilege |
| Invalid cron updates leave persisted schedule unchanged | `TestPhase4AutomationAndSubagentsAPI` | included in full API integration gate |

Focused Go tests for API security, crypto, logging, notifications and branch/bind/
quiet-hour core packages pass. Core purity scan checked the three touched packages
with no effects. Runtime-dependent provisioning remains in the root integration
gate; independent API tests exercise security behavior without depending on it.

## Scratch-copy mutation evidence

All mutations ran in an isolated copy under `.scratch/security-mutants`, never in
the working tree. Each change was restored before the next run; the copy was then
removed. The following 19 mutations each caused their regression test to fail:

- peer IP identity - caught.
- passkey expiry - caught.
- passkey bound - caught.
- hook authentication - caught.
- websocket origin - caught.
- body size bound - caught.
- secret encryption - caught.
- missing encryption key - caught.
- recursive log redaction - caught.
- notification timezone - caught.
- notification encryption - caught.
- protected branch defaults - caught.
- actual HEAD push guard - caught.
- terminal runtime selection - caught.
- event page query maximum - caught.
- database derived legacy fallback - caught.

The first actual-HEAD mutation attempt was blocked by slow/hung Windows local Git
provisioning. It was rerun against the independent persisted-agent boundary fixture
and caught. No mutation is counted merely for a compiler failure or timeout.

Linux dirfd symlink behavior, live tmux/Docker, systemd session survival, phone PWA/
passkeys, and real provider delivery require their gated/manual environment checks.
No external notification was sent: tests inject an HTTP transport. The mutation
sample prioritizes security guards; individual cron and Linux filesystem clauses
were not separately mutated in this Windows run.

## Audit persistence follow-up

`TestAPIAuditRedactsDynamicAndKnownSecrets` and
`TestAuditScrubsKnownPatternsWithoutServerWiring` pass. These cover both direct API
audit writes and the store baseline, including dynamic registered credentials, all
four stored fields, and safe reentrant callback registration. Transactional outbox
audit inserts use the same store redactor. Three additional scratch mutations were
caught:

- audit baseline token patterns - caught.
- audit dynamic server wiring - caught.
- audit persistence all-field interception - caught.
