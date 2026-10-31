# ADR 0011: Frontend verification and build tooling

Status: Accepted — 2026-10-30 audit remediation.

The previous frontend test target only type-checked, and Makefile suppressed its
failures. Components used utility classes without a utility compiler. Vite 5 and
its esbuild dependency also had reported development-server vulnerabilities.

Use Vite 8 and its matching React plugin, Tailwind 4's Vite plugin to compile the
existing utility classes, ESLint with TypeScript/rules-of-hooks, and Vitest with
Testing Library/jsdom for sequence-model and reconnect behavior tests. These are
build/test dependencies; none run in the VPS dashboard process. Node 22.22.2+ on
22.x, 24.15+ on 24.x, or 26+ is required by the locked test dependencies.
The lockfile pins resolution,
and Makefile uses npm ci when dependencies need syncing.

The terminal and Git-review views load on demand. The event view retains at most
400 items and loads history in pages of 200, subscribing before replay and merging
by persisted sequence. Reconnect replays from the last received sequence. State
updates are coalesced, with a slow foreground fallback rather than tight polling.
The service worker caches only public static assets/navigation, with a fixed cap;
API data and health responses are never cached. Notification targets stay on the
dashboard origin and select an agent through the query string.

`make verify` now fails on frontend tests, runs the Go race detector, and includes
the mechanical purity check. Automatic functional-effect verification approval
is not changed; the explicit Make target is the repository's verification path.

The Go module requires Go 1.27.3, the stable toolchain checked against
https://go.dev/dl/?mode=json on 2026-10-30. The dependency scan also found
GO-2025-3553 reachable through passkey registration; JWT v5.2.2 contains its fix.
These updates keep verification on a current standard library and remove that
reachable dependency vulnerability. Go's automatic toolchain selection can fetch
the required version when the locally installed launcher is older.

The final 2026-10-31 npm audit identified GHSA-g3hv-75wg-m249 in minimatch
10.2.6 through ESLint and typescript-eslint. A pinned override to 11.0.1 applies
the upstream fix while retaining the current lint tools; lint, tests and build
must pass with this resolution. Revisit the override when those tools adopt the
patched major version. Advisory: https://github.com/advisories/GHSA-g3hv-75wg-m249.

The self-hosted CI runner initially inherited Node 18.19.1 and failed to start
Vitest because Rolldown imports node:util.styleText. CI now selects Node 22.22.3
from the shared .nvmrc using actions/setup-node before running make verify.
This also corrects the previously understated package engine requirement to match
jsdom and its locked dependencies. It does not change the deployed Go runtime.
