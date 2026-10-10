# Sandbox Studio base image: Debian + Docker + dev tooling.
# The guest agent binary is not baked in; Studio mounts it at /opt/studio/bin/studio-agent.
# There is no init system: microsandbox's agentd stays PID 1 so that it can freeze every
# process for snapshots and forks, and `studio-agent boot` starts containerd, dockerd and
# the agent after each VM boot.
ARG DEBIAN_VERSION=trixie
FROM debian:${DEBIAN_VERSION}

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl wget git tmux sudo jq ripgrep fd-find less vim-tiny openssh-client \
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

# mise manages per-sandbox language toolchains declared in the sandbox spec.
RUN curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh

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

RUN useradd --create-home --uid 1000 --shell /bin/bash --groups sudo,docker agent \
    && mkdir -p /workspace /opt/studio/bin \
    && chown agent:agent /workspace

COPY rootfs/ /

ENV LANG=en_US.UTF-8
CMD ["/bin/bash"]
