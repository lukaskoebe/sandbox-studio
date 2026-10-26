# Sandbox Studio base image: Debian + Docker + dev tooling.
# The guest agent binary is not baked in; Studio mounts it at /opt/studio/bin/studio-agent.
# There is no init system: microsandbox's agentd stays PID 1 so that it can freeze every
# process for snapshots and forks, and `studio-agent boot` starts containerd, dockerd and
# the agent after each VM boot.
ARG DEBIAN_VERSION=trixie
FROM debian:${DEBIAN_VERSION}

ENV DEBIAN_FRONTEND=noninteractive

# bash-completion gives terminals tab completion, git's included (the git package ships its
# script): its /etc/profile.d/bash_completion.sh loads it in the login shells tmux starts,
# and the skeleton's ~/.bashrc in other interactive shells. zsh is the other shell a
# terminal can run (Settings → General → Terminal shell); agents' sessions stay in bash.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl wget git tmux sudo jq ripgrep fd-find less vim-tiny openssh-client bash-completion zsh \
        gnupg procps iproute2 iputils-ping dnsutils unzip xz-utils zstd file \
        build-essential pkg-config python3 python3-venv locales \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
        > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin \
    && sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# mise manages per-sandbox language toolchains declared in the sandbox spec. Its release tarball
# is checked against the sha256 below before it is unpacked, the same way Node is pinned.
ARG MISE_VERSION=2026.10.4
ARG MISE_SHA256_AMD64=2fc793020b442d08163603400236b2c693f8cede810c7e432fc77220e6c9e8a1
ARG MISE_SHA256_ARM64=8760841cdbf964ecf9902a50c94716c77185a99af7f8eb55c9c51ec73ecd8880
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
        amd64) arch=x64; sum="$MISE_SHA256_AMD64" ;; \
        arm64) arch=arm64; sum="$MISE_SHA256_ARM64" ;; \
        *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    tarball="mise-v${MISE_VERSION}-linux-${arch}.tar.gz"; \
    curl -fsSLo "/tmp/$tarball" "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/$tarball"; \
    echo "$sum  /tmp/$tarball" | sha256sum -c -; \
    tar -xzf "/tmp/$tarball" -C /usr/local/bin --strip-components=2 --no-same-owner mise/bin/mise; \
    rm "/tmp/$tarball"; \
    /usr/local/bin/mise --version

# Node LTS from the official tarball, checked against its published sha256, and the agent
# harnesses pinned to exact versions (docs/harnesses.md). They live in /usr/local, owned by
# root, so they don't update themselves; a new version is a new image.
ARG NODE_VERSION=24.21.0
ARG NODE_SHA256_AMD64=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6
ARG NODE_SHA256_ARM64=6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2
ARG OPENCODE_VERSION=1.18.35
ARG CLAUDE_CODE_VERSION=2.1.287
ARG CODEX_VERSION=0.162.1
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
        amd64) arch=x64; sum="$NODE_SHA256_AMD64" ;; \
        arm64) arch=arm64; sum="$NODE_SHA256_ARM64" ;; \
        *) echo "unsupported architecture" >&2; exit 1 ;; \
    esac; \
    tarball="node-v${NODE_VERSION}-linux-${arch}.tar.xz"; \
    curl -fsSLo "/tmp/$tarball" "https://nodejs.org/dist/v${NODE_VERSION}/$tarball"; \
    echo "$sum  /tmp/$tarball" | sha256sum -c -; \
    tar -xJf "/tmp/$tarball" -C /usr/local --strip-components=1 --no-same-owner \
        --exclude=CHANGELOG.md --exclude=LICENSE --exclude=README.md --exclude=include; \
    rm "/tmp/$tarball"; \
    npm install -g --no-fund --no-audit --no-update-notifier \
        "opencode-ai@${OPENCODE_VERSION}" \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
        "@openai/codex@${CODEX_VERSION}"; \
    npm cache clean --force; \
    rm -rf /root/.npm

# The ACP adapters chats run (docs/design/chat-mode.md). --omit=optional skips their own
# copies of the Claude Code and Codex binaries: CLAUDE_CODE_EXECUTABLE and CODEX_PATH point
# them at the pinned ones above. --before makes their caret-range dependencies resolve
# the same way on every build.
ARG CLAUDE_AGENT_ACP_VERSION=0.87.0
ARG CODEX_ACP_VERSION=2.1.1
RUN npm install -g --no-fund --no-audit --no-update-notifier --omit=optional \
        --before=2026-10-08T00:00:00Z \
        "@agentclientprotocol/claude-agent-acp@${CLAUDE_AGENT_ACP_VERSION}" \
        "@agentclientprotocol/codex-acp@${CODEX_ACP_VERSION}" \
    && npm cache clean --force && rm -rf /root/.npm

# Template toolchains live in /opt/mise, not in the agent's home: a sandbox's home disk
# replaces /home/agent (docs/sandboxes.md). rootfs/etc/profile.d/studio-mise.sh points
# login shells there.
RUN useradd --create-home --uid 1000 --shell /bin/bash --groups sudo,docker agent \
    && mkdir -p /workspace /opt/studio/bin /opt/mise \
    && chown agent:agent /workspace /opt/mise

COPY rootfs/ /

# zsh login shells read /etc/profile as bash's do, for the CA, the secrets and mise, then
# rootfs/etc/zsh/studio.zsh. The guest agent writes a ~/.zshrc into a home without one, so
# zsh's first-run wizard never asks.
RUN printf '\n# Sandbox Studio: what bash login shells read.\nemulate sh -c ". /etc/profile"\n' >> /etc/zsh/zprofile \
    && printf '\n# Sandbox Studio defaults.\nsource /etc/zsh/studio.zsh\n' >> /etc/zsh/zshrc

# The agent's home as the image leaves it. A new sandbox's home disk is seeded from this
# copy on its first boot, and template builds replace it with their own (docs/template-builds.md).
RUN mkdir -p /usr/share/sandbox-studio \
    && cp -a /home/agent /usr/share/sandbox-studio/home-skel \
    && chown -Rh agent:agent /usr/share/sandbox-studio/home-skel

ENV LANG=en_US.UTF-8
CMD ["/bin/bash"]
