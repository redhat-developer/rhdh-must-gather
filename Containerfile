# Stage 1: Build websocat from vendored source
# websocat v1.14.1 — update via: make vendor-update VENDOR_NAME=websocat VENDOR_VERSION=v<NEW>
# Rust compat: https://github.com/vi/websocat#rust-versions — verify after bumping either version
# https://registry.access.redhat.com/ubi9
FROM registry.access.redhat.com/ubi9:9.8-1782841664@sha256:8bf0e8f20737e9c8a68c8a498299e9504ab397b1b1f2837acb2fef12ec698f0e AS websocat-builder
RUN dnf install -y --setopt=install_weak_deps=0 --nodocs rust-toolset && \
    dnf clean all
COPY vendor/websocat /src/websocat
WORKDIR /src/websocat
RUN cargo build --release --locked \
        --no-default-features --features signal_handler,unix_stdio && \
    cp target/release/websocat /tmp/websocat && \
    /tmp/websocat --version

# Stage 2: Final image
# https://registry.access.redhat.com/ubi9-minimal
FROM registry.access.redhat.com/ubi9-minimal:9.8-1782797275@sha256:463cae32c6f6f5594b11a5c22de275016bd8545ce58a6373388e8b24f13fc15c

# Define build argument before using it in LABEL
ARG RHDH_MUST_GATHER_VERSION="0.0.0-unknown"

# Must-gather image for Red Hat Developer Hub (RHDH)
LABEL name="rhdh-must-gather" \
      vendor="Red Hat" \
      version="$RHDH_MUST_GATHER_VERSION" \
      summary="Red Hat Developer Hub (RHDH) must-gather tool" \
      description="Collects diagnostic information from RHDH deployments on Kubernetes and OpenShift clusters"

# Install system packages, CLI tools, and Python for yq
# openshift-clients: provides oc and kubectl
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
    openshift-clients \
    && microdnf clean all

# Install helm from pre-downloaded binary (no public RPM repo available;
# downloaded before hermetic build from mirror.openshift.com)
COPY bin/helm /usr/local/bin/helm

# Install Python dependencies (yq and build backends) from pinned requirements
COPY requirements-build.txt requirements.txt /tmp/
RUN pip3 install --no-cache-dir --no-deps --require-hashes \
      -r /tmp/requirements-build.txt && \
    pip3 install --no-cache-dir --no-build-isolation --require-hashes \
      -r /tmp/requirements.txt && \
    rm -f /tmp/requirements-build.txt /tmp/requirements.txt

# Copy websocat binary built from source (vendor/websocat)
COPY --from=websocat-builder /tmp/websocat /usr/local/bin/websocat

# Create non-root user for running the container
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
