FROM registry.access.redhat.com/ubi9-minimal:latest@sha256:7d4e47500f28ac3a2bff06c25eff9127ff21048538ae03ce240d57cf756acd00

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
    util-linux \
    rsync \
    && microdnf clean all

# Hermetic builds: use Hermeto generic (artifacts.lock.yaml).
# Prefetched files appear at /cachi2/output/deps/generic/<filename> during build.

# Install oc and kubectl (OpenShift CLI)
# The OpenShift client package includes both oc and kubectl
# oc is required for OpenShift-specific features like 'oc adm inspect' and routes
# renovate: datasource=custom.openshift-client
RUN GEN=/cachi2/output/deps/generic \
    OC_PREF=openshift-client-linux.tar.gz \
    && if [ -f "${GEN}/${OC_PREF}" ]; then \
         tar xzf "${GEN}/${OC_PREF}" -C /usr/local/bin/ oc kubectl; \
       else \
         curl -fsSL "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable-4.21/openshift-client-linux.tar.gz" \
           | tar xz -C /usr/local/bin/ oc kubectl; \
       fi \
    && chmod +x /usr/local/bin/oc /usr/local/bin/kubectl

# Install yq (YAML processor)
# Used for filtering manifests and processing YAML data
RUN GEN=/cachi2/output/deps/generic \
    YQ_PREF=yq_linux_amd64.tar.gz \
    && if [ -f "${GEN}/${YQ_PREF}" ]; then tar xzf "${GEN}/${YQ_PREF}"; \
       else curl -sSLo- "https://github.com/mikefarah/yq/releases/download/v4.53.2/yq_linux_amd64.tar.gz" | tar xz; fi \
    && mv -f yq_linux_amd64 /usr/local/bin/yq

# Install Helm (Kubernetes package manager)
# Required for collecting Helm-based RHDH deployments
# Installing directly from GitHub releases instead of using the install script
# to avoid dependency on openssl for checksum verification
RUN GEN=/cachi2/output/deps/generic \
    HELM_PREF=helm-linux-amd64.tar.gz \
    && if [ -f "${GEN}/${HELM_PREF}" ]; then tar xzf "${GEN}/${HELM_PREF}"; \
       else curl -fsSL "https://get.helm.sh/helm-v4.1.4-linux-amd64.tar.gz" | tar xz; fi \
    && mv linux-amd64/helm /usr/local/bin/helm \
    && rm -rf linux-amd64

# Install websocat (WebSocket CLI client)
# Required for heap dump collection via the Node.js inspector protocol
# Used to communicate with the Chrome DevTools Protocol over WebSocket
# renovate: datasource=github-releases depName=vi/websocat
RUN GEN=/cachi2/output/deps/generic \
    WS_PREF=websocat.x86_64-unknown-linux-musl \
    && if [ -f "${GEN}/${WS_PREF}" ]; then cp "${GEN}/${WS_PREF}" /usr/local/bin/websocat; \
       else curl -fsSL "https://github.com/vi/websocat/releases/download/v1.14.0/websocat.x86_64-unknown-linux-musl" -o /usr/local/bin/websocat; \
       fi \
    && chmod +x /usr/local/bin/websocat

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
