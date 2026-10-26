# ADR 0001: Library Choices for agent-dashboard

- **Status:** Proposed
- **Date:** 2026-10-26
- **Context:** Phase 0 Spike (`PLAN.md`) requires selecting core libraries for SQLite, HTTP routing, database migrations, WebAuthn, TOTP, secrets encryption, WebSockets, PTY, and system telemetry, following the architectural guidelines in `AGENTS.md` (pure Go backend, zero CGO, standard library preferred, small footprint on 2 vCPU / 2 GB RAM).

## 1. SQLite Driver

- **Decision:** `modernc.org/sqlite`
- **Rationale:**
  - Pure Go implementation (transpiled C SQLite), completely eliminating CGO requirements.
  - Enables effortless cross-compilation and running on diverse environments (Linux VPS, local dev).
  - Sufficient performance for single-user dashboard loads without external dynamic library dependencies.
- **Alternatives Considered:**
  - `github.com/mattn/go-sqlite3`: Requires CGO, C toolchain, and complicates builds and containerless deployments.

## 2. HTTP Routing and Middleware

- **Decision:** Go Standard Library `net/http` (`http.ServeMux` with Go 1.22+ routing enhancements) + small standard middleware wrappers.
- **Rationale:**
  - Go 1.22+ `http.ServeMux` natively supports HTTP method matching (`GET /api/...`, `POST /api/...`) and path parameter wildcards (`/api/repos/{id}`).
  - Standard middleware chaining is simple (`func(http.Handler) http.Handler`), requiring zero external dependencies.
- **Alternatives Considered:**
  - `github.com/go-chi/chi/v5`: Lightweight and clean, but largely redundant given Go 1.22+ stdlib capabilities.

## 3. Database Migrations

- **Decision:** Custom embedded migration runner using Go `embed.FS` + sequential SQL files (`0001_init.sql`, etc.) in `internal/store/migrations/`.
- **Rationale:**
  - Extremely lightweight (<80 lines of Go code).
  - Records applied versions atomically in a `schema_migrations` table inside SQLite transactions.
  - Eliminates heavy migration dependencies and keeps runtime memory minimal.
- **Alternatives Considered:**
  - `pressly/goose/v3` or `golang-migrate/migrate`: Full-featured but introduces substantial dependency bloat.

## 4. Authentication (Passkeys & TOTP)

- **Decisions:**
  - WebAuthn (Passkeys): `github.com/go-webauthn/webauthn`
  - TOTP: `github.com/pquerna/otp`
  - Password hashing: `golang.org/x/crypto/bcrypt`
- **Rationale:**
  - `go-webauthn/webauthn` is the battle-tested standard Go implementation of FIDO2 / WebAuthn Level 3.
  - `pquerna/otp` is the RFC 6238 standard implementation in Go, with built-in QR generation support.
  - `bcrypt` is standard, memory-safe, and available in `golang.org/x/crypto`.

## 5. Secrets at Rest Encryption

- **Decision:** Authenticated symmetric encryption using AES-256-GCM (standard library `crypto/cipher`) with key derivation via Argon2id (`golang.org/x/crypto/argon2`), with support for passphrase-derived master keys and optional `filippo.io/age` compatibility.
- **Rationale:**
  - Authenticated Encryption with Associated Data (AEAD) ensures both confidentiality and integrity/tamper detection.
  - Argon2id provides state-of-the-art protection against brute-force attacks on the master passphrase.
- **Alternatives Considered:**
  - Standalone `age` CLI invocation: external process dependency, not pure Go.

## 6. WebSockets

- **Decision:** `github.com/coder/websocket` (formerly `nhooyr.io/websocket`)
- **Rationale:**
  - Modern, idiomatic Go API designed around standard `context.Context`.
  - Zero external dependencies; actively maintained by Coder.
  - Full support for connection closure codes, ping/pong heartbeats, and streaming.
- **Alternatives Considered:**
  - `gorilla/websocket`: Older API, lacking context integration.

## 7. PTY (Pseudo-Terminal for Agent Terminal Sessions)

- **Decision:** `github.com/creack/pty` (guarded with `//go:build !windows` and Windows mock stub for local compilation).
- **Rationale:**
  - Industry standard for Unix/Linux PTY allocation in Go.
  - Direct integration with `os/exec` commands and terminal sessions.

## 8. System Telemetry (CPU, Memory, Disk)

- **Decision:** `github.com/shirou/gopsutil/v3` (or direct `/proc` parser on Linux).
- **Rationale:**
  - Provides robust, cross-platform metrics retrieval for CPU percentage, virtual memory, and disk usage for the system panel.
  - Safe, read-only system queries.

## 9. Frontend Stack

- **Decision:** React 18 / 19 + TypeScript + Vite + Tailwind CSS + Lucide Icons + `@xterm/xterm`.
- **Rationale:**
  - Mobile-first, responsive design with tiny asset footprint.
  - Production build produces static assets embedded directly into the Go binary via `//go:embed`.
  - Single binary deployment with zero runtime Node.js dependency on the target VPS.
