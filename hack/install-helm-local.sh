#!/usr/bin/env bash
#
# Install helm for local development (make local-setup).
# Prefers CGW mirror binaries; falls back to a go build from vendor/helm.
#
# Usage:
#   hack/install-helm-local.sh <version> <output_path> [os] [arch]
#
# Example:
#   hack/install-helm-local.sh 4.2.3 ./bin/helm-4.2.3-darwin-arm64/helm darwin arm64

set -euo pipefail

if [[ $# -lt 2 || $# -gt 4 ]]; then
    sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
    exit 1
fi

HELM_VERSION="${1#v}"
OUTPUT_PATH="$2"
BUILD_OS="${3:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
case "${4:-$(uname -m)}" in
    x86_64) BUILD_ARCH=amd64 ;;
    aarch64|arm64) BUILD_ARCH=arm64 ;;
    *) echo "unsupported arch: ${4:-$(uname -m)}" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CGW_SCRIPT="${ROOT}/hack/check-helm-binary-available.sh"
VENDOR_SCRIPT="${ROOT}/hack/update-vendor.sh"
HELM_SRC="${ROOT}/vendor/helm"

mkdir -p "$(dirname "${OUTPUT_PATH}")"

if "${CGW_SCRIPT}" "${HELM_VERSION}" "${BUILD_OS}" "${BUILD_ARCH}"; then
    MEMBER="helm-${BUILD_OS}-${BUILD_ARCH}"
    echo "Downloading helm v${HELM_VERSION} for ${BUILD_OS}-${BUILD_ARCH} from CGW mirror..."
    curl -fsSL \
        "https://mirror.openshift.com/pub/cgw/helm/${HELM_VERSION}/helm-${BUILD_OS}-${BUILD_ARCH}.tar.gz" \
        -o "/tmp/${MEMBER}.tar.gz"
    tar xzf "/tmp/${MEMBER}.tar.gz" -C "$(dirname "${OUTPUT_PATH}")" "${MEMBER}"
    rm -f "/tmp/${MEMBER}.tar.gz"
    mv "$(dirname "${OUTPUT_PATH}")/${MEMBER}" "${OUTPUT_PATH}"
else
    echo "CGW mirror has no helm-${BUILD_OS}-${BUILD_ARCH} for v${HELM_VERSION}; building from vendor/helm..."
    if [[ ! -f "${HELM_SRC}/go.mod" ]]; then
        echo "vendor/helm is missing; syncing helm v${HELM_VERSION}..."
        "${VENDOR_SCRIPT}" helm "v${HELM_VERSION}"
    fi
    if ! command -v go &>/dev/null; then
        echo "Error: go is required to build helm from vendor/helm" >&2
        exit 1
    fi
    build_mod=mod
    if [[ -d "${HELM_SRC}/vendor" ]]; then
        build_mod=vendor
    fi
    (cd "${HELM_SRC}" && CGO_ENABLED=0 go build "-mod=${build_mod}" -trimpath \
        -ldflags "-X helm.sh/helm/v4/internal/version.version=v${HELM_VERSION}" \
        -o "${OUTPUT_PATH}" ./cmd/helm)
fi

chmod +x "${OUTPUT_PATH}"
"${OUTPUT_PATH}" version --short
