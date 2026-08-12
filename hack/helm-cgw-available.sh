#!/usr/bin/env bash
#
# Return 0 when Helm CGW mirror artifacts exist for the requested scope.
#
# Usage:
#   hack/helm-cgw-available.sh <version>              # linux amd64 + arm64 (Konflux lockfile)
#   hack/helm-cgw-available.sh <version> <os> <arch>    # single platform tarball (local Makefile)
#
# Examples:
#   hack/helm-cgw-available.sh 4.2.3
#   hack/helm-cgw-available.sh 4.2.3 darwin arm64

set -euo pipefail

if [[ $# -lt 1 || $# -gt 3 ]]; then
    sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
    exit 2
fi

VERSION="${1#v}"
BASE_URL="https://mirror.openshift.com/pub/cgw/helm/${VERSION}"

if ! command -v curl &>/dev/null; then
    echo "Error: curl is required" >&2
    exit 2
fi

tarball_exists() {
    local os="$1" arch="$2"
    local url="${BASE_URL}/helm-${os}-${arch}.tar.gz"
    local code
    code="$(curl -fsIL -o /dev/null -w '%{http_code}' "${url}" 2>/dev/null || true)"
    [[ "${code}" == "200" ]]
}

if [[ $# -eq 3 ]]; then
    tarball_exists "$2" "$3"
    exit $?
fi

checksums="$(curl -fsSL "${BASE_URL}/sha256sum.txt" 2>/dev/null || true)"
if [[ -z "${checksums}" ]]; then
    exit 1
fi

grep -q '  helm-linux-amd64.tar.gz$' <<< "${checksums}" \
    && grep -q '  helm-linux-arm64.tar.gz$' <<< "${checksums}"
