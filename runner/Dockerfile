# syntax=docker/dockerfile:1.7
ARG RUNNER_VERSION
FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}

ARG RUNNER_VERSION
ARG YQ_VERSION=v4.44.3
ARG TARGETARCH

LABEL org.opencontainers.image.base.name="ghcr.io/actions/actions-runner:${RUNNER_VERSION}"

USER root

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        zstd \
        wget \
        curl \
        jq \
        git \
        git-lfs \
        ca-certificates \
        gnupg \
        openssh-client \
        unzip \
        zip \
        xz-utils \
        bzip2 \
        gzip \
        tar \
        build-essential \
        python3 \
        python3-pip \
        python3-venv \
        rsync \
        sudo \
        locales \
        tzdata \
        net-tools \
        dnsutils \
        iputils-ping \
        netcat-openbsd \
        file \
        less \
        vim-tiny; \
    install -d -m 0755 /etc/apt/keyrings; \
    wget -qO /etc/apt/keyrings/githubcli-archive-keyring.gpg https://cli.github.com/packages/githubcli-archive-keyring.gpg; \
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg; \
    echo "deb [arch=${TARGETARCH} signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends gh; \
    wget -qO /usr/local/bin/yq \
        "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
    chmod +x /usr/local/bin/yq; \
    sed -i 's/^# *\(en_US.UTF-8\)/\1/' /etc/locale.gen; \
    locale-gen; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

ENV LANG=C.UTF-8

USER runner
