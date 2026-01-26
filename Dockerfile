FROM registry.access.redhat.com/ubi9-minimal:latest@sha256:bb08f2300cb8d12a7eb91dddf28ea63692b3ec99e7f0fa71a1b300f2756ea829

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

# Install oc and kubectl (OpenShift CLI)
# The OpenShift client package includes both oc and kubectl
# oc is required for OpenShift-specific features like 'oc adm inspect' and routes
RUN curl -L https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz \
    | tar xz -C /usr/local/bin/ oc kubectl \
    && chmod +x /usr/local/bin/oc /usr/local/bin/kubectl \
    && oc version --client \
    && kubectl version --client

# Install yq (YAML processor)
# Used for filtering manifests and processing YAML data
RUN curl -sSLo- https://github.com/mikefarah/yq/releases/download/v4.50.1/yq_linux_amd64.tar.gz | tar xz \
    && mv -f yq_linux_amd64 /usr/local/bin/yq \
    && yq --version

# Install Helm (Kubernetes package manager)
# Required for collecting Helm-based RHDH deployments
# Installing directly from GitHub releases instead of using the install script
# to avoid dependency on openssl for checksum verification
RUN HELM_VERSION=$(curl -s https://api.github.com/repos/helm/helm/releases/latest | grep '"tag_name"' | cut -d'"' -f4) \
    && curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" -o helm.tar.gz \
    && tar xzf helm.tar.gz \
    && mv linux-amd64/helm /usr/local/bin/helm \
    && rm -rf helm.tar.gz linux-amd64 \
    && helm version

# Use our gather script in place of the original one
# Copy collection scripts
COPY collection-scripts/* /usr/bin/

RUN mv /usr/bin/must_gather /usr/bin/gather

# Set environment variable from build argument
ENV RHDH_MUST_GATHER_VERSION=$RHDH_MUST_GATHER_VERSION

ENTRYPOINT ["/usr/bin/gather"]
