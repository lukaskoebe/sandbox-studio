# Sandbox Studio browser image: Debian + Chromium + agent-browser, for a persona's browser
# VM (docs/browser.md). Agents never reach this VM: Studio drives agent-browser over the
# VM's studio-agent channel and relays the live view itself. Like the base image, the guest
# agent is not baked in; Studio mounts it at /opt/studio/bin/studio-agent, and microsandbox's
# agentd stays PID 1. There is no Docker and no sudo.
ARG DEBIAN_VERSION=trixie
FROM debian:${DEBIAN_VERSION}

ENV DEBIAN_FRONTEND=noninteractive

# Debian's chromium is built for amd64 and arm64 alike. libnss3-tools provides certutil,
# which agent-browser's --ca-cert uses to put the environment's CA into Chromium's NSS
# store (verify (live)).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl xz-utils procps \
        chromium libnss3-tools \
        fonts-liberation fonts-noto-core fonts-noto-cjk fonts-noto-color-emoji \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Node from the official tarball, checked against its published sha256 (same as the base
# image), and agent-browser pinned to an exact version: a new version is a new image.
ARG NODE_VERSION=24.21.0
ARG NODE_SHA256_AMD64=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6
ARG NODE_SHA256_ARM64=6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2
ARG AGENT_BROWSER_VERSION=0.39.0
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
    npm install -g --no-fund --no-audit --no-update-notifier "agent-browser@${AGENT_BROWSER_VERSION}"; \
    npm cache clean --force; \
    rm -rf /root/.npm; \
    test -x /usr/local/bin/agent-browser; \
    agent-browser --version

# agent-browser runs as the agent user; its profile and downloads live on the VM's owned
# disk (/workspace), so logins persist across restarts.
RUN useradd --create-home --uid 1000 --shell /bin/bash agent \
    && mkdir -p /workspace /opt/studio/bin \
    && chown agent:agent /workspace

COPY rootfs/ /

ENV LANG=C.UTF-8
CMD ["/bin/bash"]
