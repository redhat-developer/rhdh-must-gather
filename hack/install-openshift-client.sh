#!/usr/bin/env bash
#
# Install oc and kubectl from openshift-client tarballs to /usr/local/bin.
#
# Curl mode (upstream Containerfile):
#   hack/install-openshift-client.sh
#
# Prefetch mode (Konflux hermetic):
#   hack/install-openshift-client.sh --prefetch

set -euo pipefail

PREFETCH=false
if [[ "${1:-}" == "--prefetch" ]]; then
    PREFETCH=true
fi

if [[ -n "${TARGETPLATFORM:-}" ]]; then
    BUILD_ARCH="$(cut -d/ -f2 <<< "${TARGETPLATFORM}")"
else
    case "$(uname -m)" in
        x86_64) BUILD_ARCH=amd64 ;;
        aarch64|arm64) BUILD_ARCH=arm64 ;;
        *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
    esac
fi

case "${BUILD_ARCH}" in
    amd64) TARBALL="openshift-client-linux.tar.gz" ;;
    arm64) TARBALL="openshift-client-linux-arm64.tar.gz" ;;
    *) echo "unsupported arch: ${BUILD_ARCH}" >&2; exit 1 ;;
esac

extract_clients() {
    local archive="$1"
    tar xzf "${archive}" -C /usr/local/bin oc kubectl
    chmod +x /usr/local/bin/oc /usr/local/bin/kubectl
}

if ${PREFETCH}; then
    # shellcheck disable=SC1091
    . /cachi2/cachi2.env
    extract_clients "/cachi2/output/deps/generic/${TARBALL}"
else
    curl -fsSL "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable-4.21/${TARBALL}" \
        -o "/tmp/${TARBALL}"
    extract_clients "/tmp/${TARBALL}"
    rm -f "/tmp/${TARBALL}"
fi

oc version --client
kubectl version --client
