# Stage 1: Build Go gather binary
# https://registry.access.redhat.com/ubi10/go-toolset
FROM registry.access.redhat.com/ubi10/go-toolset:10.2-1791462472@sha256:7ec4dc8858bd3467b5e13ecccc9f913a0908978e50420958922d4494bf7b65b9 AS go-builder
COPY go.mod go.sum /opt/app-root/src/
WORKDIR /opt/app-root/src
RUN go mod download
COPY cmd/ /opt/app-root/src/cmd/
COPY internal/ /opt/app-root/src/internal/
ARG RHDH_MUST_GATHER_VERSION="0.0.0-unknown"
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X 'github.com/redhat-developer/rhdh-must-gather/internal/cli.version=${RHDH_MUST_GATHER_VERSION}'" \
    -o /tmp/gather ./cmd/gather

# Stage 2: Final image
# https://registry.access.redhat.com/ubi10-minimal
FROM registry.access.redhat.com/ubi10-minimal:10.2-1791444377@sha256:bcecd3e74c9d03eb1a596c6c8f366775a289d422ac5925ec1539924d762ebf23

ARG RHDH_MUST_GATHER_VERSION="0.0.0-unknown"

# Must-gather image for Red Hat Developer Hub (RHDH)
LABEL name="rhdh-must-gather" \
      vendor="Red Hat" \
      version="$RHDH_MUST_GATHER_VERSION" \
      summary="Red Hat Developer Hub (RHDH) must-gather tool" \
      description="Collects diagnostic information from RHDH deployments on Kubernetes and OpenShift clusters"

# Install minimal runtime dependencies:
# tar, gzip, rsync: required by `oc adm must-gather` to copy/compress output from the pod
# util-linux: provides setsid (required by `oc adm must-gather`)
RUN microdnf install -y --setopt=install_weak_deps=0 --nodocs \
    gzip \
    tar \
    util-linux \
    rsync \
    && microdnf clean all

# Create non-root user for running the container
RUN microdnf install -y --setopt=install_weak_deps=0 --nodocs shadow-utils \
    && groupadd -g 1001 must-gather \
    && useradd -u 1001 -g must-gather -s /bin/bash -m must-gather \
    && microdnf remove -y shadow-utils \
    && microdnf clean all

# Copy Go gather binary — all collection logic is built in, no external tools needed
COPY --from=go-builder /tmp/gather /usr/bin/gather

# Set environment variable from build argument
ENV RHDH_MUST_GATHER_VERSION=$RHDH_MUST_GATHER_VERSION

# Run as non-root user
USER 1001

ENTRYPOINT ["/usr/bin/gather"]
