# Stage 1: Build websocat from vendored source
# websocat v1.14.1 — update via: make vendor-update VENDOR_NAME=websocat VENDOR_VERSION=v<NEW>
# Rust compat: https://github.com/vi/websocat#rust-versions — verify after bumping either version
# https://registry.access.redhat.com/ubi9
FROM registry.access.redhat.com/ubi9:9.8-1786339177@sha256:9d99826a5299a54fa92e9b47d74e6cd72efb04cb57eb8fe748ed93e08ebb6184 AS websocat-builder
RUN dnf install -y --setopt=install_weak_deps=0 --nodocs rust-toolset && \
    dnf clean all
COPY vendor/websocat /src/websocat
WORKDIR /src/websocat
RUN cargo build --release \
        --no-default-features --features signal_handler,unix_stdio && \
    cp target/release/websocat /tmp/websocat && \
    /tmp/websocat --version

# Stage 2: Install helm from Red Hat CGW mirror
# helm v4.2.3 — replace this stage with go-toolset + vendor/helm build if CGW lacks a newer release
# https://registry.access.redhat.com/ubi9-minimal
FROM registry.access.redhat.com/ubi9-minimal:9.8-1786380870@sha256:7c372902c8d211db2d25c8277ba534a73b92742a334874dced829a63b0f21221 AS helm-builder
ARG TARGETPLATFORM
COPY Makefile /tmp/Makefile
COPY hack/install-helm-cgw-binary.sh /tmp/install-helm-cgw-binary.sh
RUN microdnf install -y --setopt=install_weak_deps=0 --nodocs tar gzip bash \
    && microdnf clean all \
    && HELM_VERSION=$(grep '^HELM_VERSION' /tmp/Makefile | sed 's/.*:= *//') \
    && CONTAINER_BUILD=true TARGETPLATFORM="${TARGETPLATFORM}" HELM_VERSION="${HELM_VERSION}" \
       bash /tmp/install-helm-cgw-binary.sh \
    && rm -f /tmp/Makefile /tmp/install-helm-cgw-binary.sh

# Stage 3: Final image
# https://registry.access.redhat.com/ubi9-minimal
FROM registry.access.redhat.com/ubi9-minimal:9.8-1786380870@sha256:7c372902c8d211db2d25c8277ba534a73b92742a334874dced829a63b0f21221

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
# argcomplete 3.7+ uses PEP 604 union types (str | bytes) in class-level
# annotations, which Python 3.9 evaluates at class definition time and fails.
# The downstream (hermetic) Containerfile is not affected because it pins
# argcomplete via hash-locked requirements.txt generated with --python-version=3.9.
RUN YQ_VERSION=$(grep '^YQ_VERSION' /tmp/Makefile | sed 's/.*:= *//') \
    && pip3 install --no-cache-dir "yq==${YQ_VERSION}" "argcomplete<3.7" \
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

# Copy helm binary from CGW mirror (helm-builder stage)
COPY --from=helm-builder /tmp/helm /usr/local/bin/helm

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
