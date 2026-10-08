# Sandbox Studio base image: Debian + systemd + Docker + dev tooling.
# The guest agent binary is not baked in; Studio mounts it at /opt/studio/bin/studio-agent.
ARG DEBIAN_VERSION=trixie
FROM debian:${DEBIAN_VERSION}

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        systemd systemd-sysv dbus ca-certificates \
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

RUN useradd --create-home --uid 1000 --shell /bin/bash --groups sudo,docker agent \
    && mkdir -p /workspace /opt/studio/bin \
    && chown agent:agent /workspace

COPY rootfs/ /

# Units that make no sense in a microVM, plus first-boot noise.
RUN systemctl mask getty@tty1.service serial-getty@ttyS0.service systemd-logind.service \
        systemd-timesyncd.service apt-daily.timer apt-daily-upgrade.timer e2scrub_reap.service \
        systemd-modules-load.service systemd-sysctl.service \
    && systemctl enable docker.service containerd.service studio-agent.service studio-ca.service

ENV LANG=en_US.UTF-8
CMD ["/lib/systemd/systemd"]
