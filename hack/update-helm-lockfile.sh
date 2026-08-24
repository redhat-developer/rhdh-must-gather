#!/usr/bin/env bash
#
# Refresh artifacts.lock.yaml for Helm CGW binaries and bump Makefile HELM_VERSION.
#
# Usage:
#   hack/update-helm-lockfile.sh v4.2.3
#
# Checksums are read from mirror.openshift.com/pub/cgw/helm/<version>/sha256sum.txt.
#

set -euo pipefail

if [[ $# -ne 1 ]]; then
    sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
    exit 1
fi

VERSION="$1"
VERSION_NUM="${VERSION#v}"
BASE_URL="https://mirror.openshift.com/pub/cgw/helm/${VERSION_NUM}"

if ! command -v curl &>/dev/null; then
    echo "Error: curl is required"
    exit 1
fi

cd "$(git rev-parse --show-toplevel)"

if ! ./hack/check-helm-binary-available.sh "${VERSION_NUM}"; then
    echo "Error: CGW mirror has no helm v${VERSION_NUM} linux-amd64/arm64 binaries." >&2
    echo "Use vendored source instead:" >&2
    echo "  make vendor-update VENDOR_NAME=helm VENDOR_VERSION=${VERSION}" >&2
    echo "Then switch Containerfile helm-builder to go-toolset + vendor/helm (see draft PR #282)." >&2
    exit 1
fi

echo "Fetching checksums from ${BASE_URL}/sha256sum.txt..."
CHECKSUMS="$(curl -fsSL "${BASE_URL}/sha256sum.txt")"

checksum_for() {
    local file="$1"
    local hash
    hash="$(grep "  ${file}$" <<< "${CHECKSUMS}" | awk '{print $1}')"
    if [[ -z "${hash}" ]]; then
        echo "Error: no checksum for ${file} in ${BASE_URL}/sha256sum.txt" >&2
        exit 1
    fi
    printf 'sha256:%s' "${hash}"
}

cat > artifacts.lock.yaml <<EOF
---
metadata:
  version: "1.0"
artifacts:
  - download_url: ${BASE_URL}/helm-linux-amd64.tar.gz
    checksum: $(checksum_for helm-linux-amd64.tar.gz)
    filename: helm-linux-amd64.tar.gz
  - download_url: ${BASE_URL}/helm-linux-arm64.tar.gz
    checksum: $(checksum_for helm-linux-arm64.tar.gz)
    filename: helm-linux-arm64.tar.gz
EOF

sed -i.bak "s|^HELM_VERSION := .*|HELM_VERSION := ${VERSION_NUM}|" Makefile && rm -f Makefile.bak

echo "Updated artifacts.lock.yaml and Makefile HELM_VERSION to ${VERSION_NUM}"
