#!/usr/bin/env bash
#
# Install helm from a Red Hat CGW mirror binary tarball to /tmp/helm.
#
# Curl mode (upstream Containerfile helm-builder):
#   HELM_VERSION=4.2.3 CONTAINER_BUILD=true TARGETPLATFORM=linux/arm64 \
#     hack/install-helm-binary.sh
#
# Prefetch mode (Konflux hermetic helm-builder):
#   CONTAINER_BUILD=true TARGETPLATFORM=linux/arm64 \
#     hack/install-helm-binary.sh --prefetch
#
# Local Makefile uses OS/ARCH from the host (darwin or linux) without CONTAINER_BUILD.

set -euo pipefail

PREFETCH=false
if [[ "${1:-}" == "--prefetch" ]]; then
    PREFETCH=true
fi

if [[ -n "${TARGETPLATFORM:-}" ]]; then
    BUILD_OS="${TARGETPLATFORM%%/*}"
    BUILD_ARCH="${TARGETPLATFORM##*/}"
else
    BUILD_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$(uname -m)" in
        x86_64) BUILD_ARCH=amd64 ;;
        aarch64|arm64) BUILD_ARCH=arm64 ;;
        *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
    esac
fi

case "${BUILD_ARCH}" in
    aarch64) BUILD_ARCH=arm64 ;;
esac

if [[ "${CONTAINER_BUILD:-}" == "true" ]]; then
    case "${BUILD_OS}" in
        linux) ;;
        *) echo "unsupported OS for container image: ${BUILD_OS} (expected linux)" >&2; exit 1 ;;
    esac
fi

case "${BUILD_ARCH}" in
    amd64|arm64) ;;
    *) echo "unsupported arch: ${BUILD_ARCH}" >&2; exit 1 ;;
esac

TARBALL="helm-${BUILD_OS}-${BUILD_ARCH}.tar.gz"
MEMBER="helm-${BUILD_OS}-${BUILD_ARCH}"

extract_helm() {
    local archive="$1"
    tar xzf "${archive}" -C /tmp "${MEMBER}"
    mv "/tmp/${MEMBER}" /tmp/helm
}

if ${PREFETCH}; then
    # shellcheck disable=SC1091
    . /cachi2/cachi2.env
    extract_helm "/cachi2/output/deps/generic/${TARBALL}"
else
    if [[ -z "${HELM_VERSION:-}" ]]; then
        echo "HELM_VERSION is required for curl mode" >&2
        exit 1
    fi
    curl -fsSL "https://mirror.openshift.com/pub/cgw/helm/${HELM_VERSION}/${TARBALL}" \
        -o "/tmp/${TARBALL}"
    extract_helm "/tmp/${TARBALL}"
    rm -f "/tmp/${TARBALL}"
fi

chmod +x /tmp/helm
/tmp/helm version
