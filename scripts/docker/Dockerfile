# Dockerfile for agent-dashboard sandbox containers
# Runs non-root as user agentd (UID 1001) with tmux, git, and Node toolchains
FROM debian:bookworm-slim

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
    tmux \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js (LTS v20) for CLI harnesses (Claude Code, etc.)
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user agentd matching host UID 1001
RUN useradd -m -u 1001 -s /bin/bash agentd \
    && mkdir -p /workspace \
    && chown -R agentd:agentd /workspace /home/agentd

USER agentd
WORKDIR /workspace

CMD ["tail", "-f", "/dev/null"]
