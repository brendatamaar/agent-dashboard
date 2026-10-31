# ADR 0009: Harden remote execution boundaries and secret storage

Status: accepted under the authorized audit remediation (2026-10-30).

The dashboard is a remote execution surface. HTTP throttling keys now use the
normalized TCP peer address; forwarding headers are not trusted. API bodies are
bounded at 1 MiB. Passkey challenges expire after five minutes, are single use,
bind to their ceremony/user, and have a fixed capacity. Initial setup is atomic.
WebSocket requests validate the configured exact origin before upgrade, periodically
revalidate their session, use the selected agent runtime (explicit host runtime for
the general shell), write terminal audit records, and close on server shutdown.
The systemd unit leaves durable host tmux sessions alive when the dashboard exits.

A separate owner-only `<database>.key` file holds a random AES-256 key. SQLite no
longer contains enough information to decrypt stored secrets. Back up this file
separately and restore it together with SQLite; a missing key with v2 ciphertext is
a startup error. Windows development requires equivalent account ACL protection;
Unix deployments enforce mode 0600. Existing explicit-passphrase ciphertext can be
migrated on first read using that original passphrase and the configured owner,
regardless of username. Legacy records encrypted with the database password hash
are not silently decrypted: re-enter those credentials through Settings after
upgrade, or perform an explicit offline migration with the old key. Undecryptable
records remain intact. Legacy plaintext notification JSON is encrypted in place,
preserving VAPID identity and channel settings. All new secret records use a v2
envelope; notification secrets use the same vault without importing the API.

Bind policy accepts literal loopback/Tailscale addresses and localhost. A `.ts.net`
suffix alone no longer establishes trust: DNS labels are not proof of an address
range. Use a literal Tailscale IP or loopback with Tailscale Serve. This tightens the
previous heuristic while preserving the PRD's network isolation requirement.

Subagent file IO uses Linux directory descriptors with O_NOFOLLOW and atomic rename,
so a repository-controlled symlink cannot redirect dashboard reads or writes.
Non-Linux code is a development fallback that rejects symlink ancestors; Linux is
the production runtime. No additional dependencies are introduced (x/sys was already
present). Timezone loading moves to the notification shell; core functions receive
an already-localized instant. Protected branch defaults are fresh values, so callers
cannot mutate global security policy.

Audit persistence also sanitizes actor, action, target and result at the SQLite
boundary, for both direct API writes and transactional orchestration commands.
Known token patterns apply without server wiring. The server supplies the shared
dynamic-secret registry through a synchronized callback; callbacks execute outside
the registry configuration lock. Regression and isolated mutation evidence is
recorded in `docs/design/security-validation.md`.
