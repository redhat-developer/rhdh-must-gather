# Stage 1: Build websocat from vendored source
# websocat v1.14.1 — update via: make vendor-update VENDOR_NAME=websocat VENDOR_VERSION=v<NEW>
# Rust compat: https://github.com/vi/websocat#rust-versions — verify after bumping either version
FROM registry.access.redhat.com/ubi9:9.7-1777858048@sha256:38952545b5a9ddf145152f37b0da702d7d75fac1ddc52c5e14b03c7a77c51586 AS websocat-builder
RUN dnf install -y --setopt=install_weak_deps=0 --nodocs rust-toolset && \
    dnf clean all
COPY vendor/websocat /src/websocat
WORKDIR /src/websocat
RUN cargo build --release \
        --no-default-features --features signal_handler,unix_stdio && \
    cp target/release/websocat /tmp/websocat && \
    /tmp/websocat --version

# Stage 2: Final image
FROM registry.access.redhat.com/ubi9-minimal:9.7-1777857961@sha256:8d0a8fb39ec907e8ca62cdd24b62a63ca49a30fe465798a360741fde58437a23

# Define build argument before using it in LABEL
ARG RHDH_MUST_GATHER_VERSION="0.0.0-unknown"

# Must-gather image for Red Hat Developer Hub (RHDH)
LABEL name="rhdh-must-gather" \
      vendor="Red Hat" \
      version="$RHDH_MUST_GATHER_VERSION" \
      summary="Red Hat Developer Hub (RHDH) must-gather tool" \
      description="Collects diagnostic information from RHDH deployments on Kubernetes and OpenShift clusters"

# Install basic tools and dependencies needed for must-gather operations
# Note: UBI9-minimal already has curl-minimal and coreutils-single installed
# We use --setopt=install_weak_deps=0 to avoid unnecessary dependencies
# and --nodocs to reduce image size
# findutils: provides find, xargs
# grep, sed: text processing used in sanitization and data collection
# jq: JSON processing (validated in common.sh)
# python3, python3-pip: required for yq (kislyuk/yq — jq wrapper for YAML)
# util-linux: provides setsid (required by oc adm must-gather)
# rsync: file synchronization tool (required by oc adm must-gather)
RUN microdnf install -y --setopt=install_weak_deps=0 --nodocs \
    tar \
    gzip \
    bash \
    findutils \
    grep \
    sed \
    jq \
    python3 \
    python3-pip \
    util-linux \
    rsync \
    && microdnf clean all
COPY Makefile /tmp/Makefile
RUN YQ_VERSION=$(grep '^YQ_VERSION' /tmp/Makefile | sed 's/.*:= *//') \
    && pip3 install --no-cache-dir "yq==${YQ_VERSION}" \
    && rm /tmp/Makefile

# Install oc and kubectl (OpenShift CLI)
# The OpenShift client package includes both oc and kubectl
# oc is required for OpenShift-specific features like 'oc adm inspect' and routes
# renovate: datasource=custom.openshift-client
RUN curl -L https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable-4.21/openshift-client-linux.tar.gz \
    | tar xz -C /usr/local/bin/ oc kubectl \
    && chmod +x /usr/local/bin/oc /usr/local/bin/kubectl \
    && oc version --client \
    && kubectl version --client

# Install Helm (Kubernetes package manager)
# Required for collecting Helm-based RHDH deployments
# Installing directly from GitHub releases instead of using the install script
# to avoid dependency on openssl for checksum verification
RUN curl -fsSL "https://get.helm.sh/helm-v4.1.4-linux-amd64.tar.gz" -o helm.tar.gz \
    && tar xzf helm.tar.gz \
    && mv linux-amd64/helm /usr/local/bin/helm \
    && rm -rf helm.tar.gz linux-amd64 \
    && helm version

# Copy websocat binary built from source (vendor/websocat)
COPY --from=websocat-builder /tmp/websocat /usr/local/bin/websocat

# Create non-root user for running the container
# Using UID 1001 which is commonly used and works well with OpenShift's arbitrary UID assignment
RUN microdnf install -y --setopt=install_weak_deps=0 --nodocs shadow-utils \
    && groupadd -g 1001 must-gather \
    && useradd -u 1001 -g must-gather -s /bin/bash -m must-gather \
    && microdnf remove -y shadow-utils \
    && microdnf clean all

# Use our gather script in place of the original one
# Copy collection scripts
COPY collection-scripts/* /usr/bin/

RUN mv /usr/bin/must_gather /usr/bin/gather

# Set environment variable from build argument
ENV RHDH_MUST_GATHER_VERSION=$RHDH_MUST_GATHER_VERSION

# Run as non-root user
USER 1001

ENTRYPOINT ["/usr/bin/gather"]
