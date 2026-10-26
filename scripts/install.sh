#!/usr/bin/env bash
set -euo pipefail

# install.sh — Setup agent-dashboard on Ubuntu 24.04 VPS
# Must be run as root (or via sudo).

if [[ $EUID -ne 0 ]]; then
   echo "Error: This script must be run as root or with sudo." >&2
   exit 1
fi

echo "==> Setting up agent-dashboard environment..."

# 1. Create dedicated agentd user and group if not present
if ! id -u agentd >/dev/null 2>&1; then
    echo "Creating system user 'agentd'..."
    useradd -r -s /usr/sbin/nologin -m -d /var/lib/agent-dashboard agentd
else
    echo "User 'agentd' already exists."
fi

# Add agentd to docker group if docker is installed
if getent group docker >/dev/null 2>&1; then
    echo "Adding 'agentd' to 'docker' group..."
    usermod -aG docker agentd
    echo "NOTE: 'docker' group grants root-equivalent permissions to agentd."
fi

# 2. Create state and configuration directories
mkdir -p /var/lib/agent-dashboard
mkdir -p /etc/agent-dashboard

chown -R agentd:agentd /var/lib/agent-dashboard
chmod 700 /var/lib/agent-dashboard

chown -R agentd:agentd /etc/agent-dashboard
chmod 700 /etc/agent-dashboard

# 3. Create default environment configuration if not present
ENV_FILE="/etc/agent-dashboard/agent-dashboard.env"
if [[ ! -f "$ENV_FILE" ]]; then
    echo "Generating default configuration at $ENV_FILE..."
    cat > "$ENV_FILE" <<'EOF'
# agent-dashboard configuration
# Bind strictly to Tailscale or loopback. Never bind to 0.0.0.0.
AGENTD_BIND=127.0.0.1:8080
AGENTD_DATA_DIR=/var/lib/agent-dashboard
AGENTD_LOG_LEVEL=info

# Master passphrase used to derive AES-256 encryption key for stored secrets at rest
# Set a strong passphrase below:
# AGENTD_MASTER_PASSPHRASE=your-strong-master-passphrase-here

# WebAuthn Passkey settings (set to your Tailscale MagicDNS name or custom host)
AGENTD_RP_ID=localhost
AGENTD_RP_ORIGIN=http://localhost:8080
EOF
    chmod 600 "$ENV_FILE"
    chown agentd:agentd "$ENV_FILE"
fi

# 4. Install binary if compiled in current directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

if [[ -f "$REPO_DIR/agent-dashboard" ]]; then
    echo "Installing binary to /usr/local/bin/agent-dashboard..."
    install -m 755 "$REPO_DIR/agent-dashboard" /usr/local/bin/agent-dashboard
elif [[ -f "./agent-dashboard" ]]; then
    echo "Installing binary to /usr/local/bin/agent-dashboard..."
    install -m 755 "./agent-dashboard" /usr/local/bin/agent-dashboard
else
    echo "Notice: 'agent-dashboard' binary not found in current directory. Compile with 'make build' first."
fi

# 5. Install systemd service unit
echo "Installing systemd service unit..."
cp "$SCRIPT_DIR/agent-dashboard.service" /etc/systemd/system/agent-dashboard.service
systemctl daemon-reload

echo ""
echo "=========================================================="
echo " agent-dashboard installation complete!"
echo "=========================================================="
echo ""
echo "Next steps:"
echo "1. Verify Tailscale is running: tailscale status"
echo "2. If using Tailscale Serve (recommended for HTTPS & Passkeys):"
echo "     tailscale serve --bg 8080"
echo "   Or update /etc/agent-dashboard/agent-dashboard.env with your Tailscale IP:"
echo "     AGENTD_BIND=100.x.y.z:8080"
echo "     AGENTD_RP_ID=your-node.ts.net"
echo "     AGENTD_RP_ORIGIN=https://your-node.ts.net"
echo ""
echo "3. Enable and start the systemd service:"
echo "     sudo systemctl enable --now agent-dashboard"
echo ""
echo "4. Check service status and logs:"
echo "     sudo systemctl status agent-dashboard"
echo "     sudo journalctl -u agent-dashboard -f"
echo ""
echo "5. Verify listening ports (confirm NO public 0.0.0.0 bind):"
echo "     ss -tlnp | grep agent-dashboard"
echo ""
