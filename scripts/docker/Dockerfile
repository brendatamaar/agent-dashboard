# Dockerfile for agent-dashboard sandbox containers
# Runs non-root as the configured agentd UID/GID with tmux and CLI toolchains
FROM node:22-bookworm-slim

# Avoid interactive prompts during apt install
ENV DEBIAN_FRONTEND=noninteractive

# Install essential dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    gnupg \
    jq \
    openssh-client \
    procps \
    python3 \
    tmux \
    && rm -rf /var/lib/apt/lists/*

# Pin supported CLI packages at build time; later harnesses require a custom
# image until their proprietary installers are explicitly configured.
ARG CLAUDE_VERSION=2.1.314
ARG CODEX_VERSION=0.166.0
ARG GEMINI_VERSION=0.66.0
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION} @openai/codex@${CODEX_VERSION} @google/gemini-cli@${GEMINI_VERSION} \
    && claude --version && codex --version && gemini --version
COPY supervisor.py /usr/local/lib/agentd-supervisor.py
RUN chmod 0444 /usr/local/lib/agentd-supervisor.py

# Match the dedicated service account without changing host account ownership.
ARG AGENTD_UID=1001
ARG AGENTD_GID=1001
LABEL agent-dashboard.uid=${AGENTD_UID} agent-dashboard.gid=${AGENTD_GID}
RUN userdel node && (groupdel node || true) \
    && groupadd -g ${AGENTD_GID} agentd \
    && useradd -m -u ${AGENTD_UID} -g ${AGENTD_GID} -s /bin/bash agentd \
    && mkdir -p /workspace \
    && chown -R agentd:agentd /workspace /home/agentd

USER agentd
WORKDIR /workspace

CMD ["tail", "-f", "/dev/null"]
