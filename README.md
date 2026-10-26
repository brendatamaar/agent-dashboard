# agent-dashboard

`agent-dashboard` is a personal, self-hosted web dashboard that runs on a small Linux VPS (2 vCPU, 2 GB RAM). It manages git repositories, background coding agents (Claude Code first; Codex CLI, Gemini CLI, and Meta's Muse Code later), and git workflows from a laptop or phone.

---

## Architecture & Security Model

- **Functional Core / Imperative Shell:** Stateful domain logic (rate limiter, secret scrubbing, bind validation, agent lifecycle) is pure Go with zero IO or external side effects, making it deterministic and unit/property testable. All IO (SQLite, network, tmux, Docker) lives strictly in the imperative shell.
- **Strict Bind Policy:** The dashboard refuses to start on `0.0.0.0` or public interfaces unless explicitly passed `--insecure-allow-public-bind`. It binds only to loopback (`127.0.0.1`) or the Tailscale interface (`100.64.0.0/10`).
- **Authentication:** FIDO2 / WebAuthn Passkeys primary with password + TOTP fallback, rate-limited login, CSRF protection, and short-lived HTTP-only sessions.
- **Encrypted Secrets:** Stored tokens (GitHub fine-grained token, Anthropic API key, etc.) are encrypted at rest with authenticated AES-256-GCM and Argon2id key derivation. Plaintext tokens are automatically scrubbed from structured logs and transcripts.
- **Single Process:** Single Go binary with embedded React + TypeScript PWA (`go:embed`), running under systemd as a dedicated non-root user `agentd`.

---

## VPS Setup (Ubuntu 24.04 on 2 vCPU / 2 GB RAM)

### 1. Configure Swap File (2–4 GB)

A 2 GB VPS requires a swap file so that background agents, builds, and toolchains do not trigger out-of-memory (OOM) kills.

```bash
# Create a 4 GB swap file
sudo fallocate -l 4G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# Make swap permanent across reboots
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# Verify swap status
free -h
```

### 2. Tailscale Setup

Install and authenticate Tailscale on your VPS:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up

# Check your Tailscale IP and MagicDNS hostname
tailscale ip -4
tailscale status
```

#### Option A: Tailscale Serve (Recommended)
Tailscale Serve provides valid HTTPS certificates automatically, which is ideal for WebAuthn Passkeys and PWA installation:

```bash
# Forward HTTPS from your Tailscale node name to the local dashboard port
tailscale serve --bg 8080
```

#### Option B: Direct Tailscale IP Bind
Alternatively, bind `agent-dashboard` directly to your Tailscale IP:

```bash
AGENTD_BIND=100.x.y.z:8080
```

---

## Installation & Deployment

### 1. Build and Install on VPS

```bash
# Clone the repo on your VPS
git clone <repo-url>
cd agent-dashboard

# Build frontend and binary
make verify

# Run automated install script (creates agentd user, directories, systemd service)
sudo ./scripts/install.sh
```

### 2. Configure Environment

Edit `/etc/agent-dashboard/agent-dashboard.env`:

```ini
AGENTD_BIND=127.0.0.1:8080
AGENTD_DATA_DIR=/var/lib/agent-dashboard
AGENTD_LOG_LEVEL=info
AGENTD_MASTER_PASSPHRASE="your-strong-passphrase"
AGENTD_RP_ID=your-node.ts.net
AGENTD_RP_ORIGIN=https://your-node.ts.net
```

### 3. Claude Code Setup on Headless VPS

To run Claude Code agents from the dashboard, configure authentication on the VPS:

#### Option A: Anthropic API Key (Recommended for automated/unattended workloads)
Store your Anthropic API Key in the dashboard under Settings -> Secrets as `anthropic_api_key`, or export it in `/etc/agent-dashboard/agent-dashboard.env`:
```ini
ANTHROPIC_API_KEY="sk-ant-api..."
```

#### Option B: Claude Subscription (Pro / Team)
1. On your local machine with a browser, generate a long-lived auth token:
   ```bash
   claude setup-token
   ```
2. Save the resulting token in the dashboard encrypted secrets as `claude_token` (or copy credentials to `/home/agentd/.claude/` with `chmod 600`).
3. **Note on Provider Terms:** Verify Anthropic's current terms of service when using subscription tokens in headless server environments.

### 4. Start the Service

```bash
sudo systemctl enable --now agent-dashboard
sudo systemctl status agent-dashboard
```

Verify that the dashboard is listening **strictly** on the loopback or Tailscale IP:

```bash
ss -tlnp | grep agent-dashboard
# MUST output 127.0.0.1:8080 or 100.x.y.z:8080.
# MUST NOT show 0.0.0.0:8080 or :::8080.
```

---

## Development & Testing

```bash
# Run backend and frontend in dev mode
make dev

# Run all unit tests
make test

# Run linter and type-checks
make lint

# Verify full build, lint, and test suite
make verify
```

---

## Gate 0 Verification from Phone

1. Connect your phone to Tailscale.
2. Open your browser and navigate to your Tailscale domain (e.g. `https://your-node.ts.net` or `http://100.x.y.z:8080`).
3. Complete the **First-Run Setup**: choose an admin username and password, and save your TOTP secret.
4. (Optional) Tap **Add Passkey** to register Face ID, Touch ID, or your phone's biometric passkey.
5. In your browser's menu, tap **Add to Home Screen** to install the PWA.
6. Check the **System Panel**: confirm CPU, RAM, Disk, and Swap metrics reflect your VPS accurately.
7. Verify that non-local or public interfaces cannot reach the server:
   ```bash
   ss -tlnp
   ```
