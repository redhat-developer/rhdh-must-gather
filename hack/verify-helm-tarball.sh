#!/usr/bin/env bash
#
# Verify a Helm CGW tarball against artifacts.lock.yaml or mirror sha256sum.txt.
#
# Usage:
#   hack/verify-helm-tarball.sh <archive> <filename> [lockfile] [helm_version]
#
# Prefer a pinned checksum from lockfile when the filename is listed there
# (linux/amd64, linux/arm64). Otherwise require helm_version and verify against
# https://mirror.openshift.com/pub/cgw/helm/<version>/sha256sum.txt.

set -euo pipefail

if [[ $# -lt 2 || $# -gt 4 ]]; then
    sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
    exit 1
fi

ARCHIVE="$1"
FILENAME="$2"
LOCKFILE="${3:-}"
HELM_VERSION="${4:-}"

if [[ ! -f "${ARCHIVE}" ]]; then
    echo "Error: archive not found: ${ARCHIVE}" >&2
    exit 1
fi

file_sha256() {
    local file="$1"
    if command -v sha256sum &>/dev/null; then
        sha256sum "${file}" | awk '{print $1}'
    elif command -v shasum &>/dev/null; then
        shasum -a 256 "${file}" | awk '{print $1}'
    else
        echo "Error: need sha256sum or shasum to verify ${file}" >&2
        exit 1
    fi
}

expected_from_lockfile() {
    local lockfile="$1"
    local filename="$2"
    local checksum=""
    local line c f
    while IFS= read -r line || [[ -n "${line}" ]]; do
        case "${line}" in
            *"checksum:"*)
                c="${line#*checksum:}"
                c="${c#"${c%%[![:space:]]*}"}"
                c="${c%"${c##*[![:space:]]}"}"
                c="${c#sha256:}"
                ;;
            *"filename:"*)
                f="${line#*filename:}"
                f="${f#"${f%%[![:space:]]*}"}"
                f="${f%"${f##*[![:space:]]}"}"
                if [[ "${f}" == "${filename}" && -n "${c}" ]]; then
                    checksum="${c}"
                    break
                fi
                ;;
        esac
    done < "${lockfile}"
    printf '%s' "${checksum}"
}

expected_from_mirror() {
    local version="$1"
    local filename="$2"
    local checksums hash
    checksums="$(curl -fsSL "https://mirror.openshift.com/pub/cgw/helm/${version}/sha256sum.txt")"
    hash="$(grep "  ${filename}$" <<< "${checksums}" | awk '{print $1}')"
    if [[ -z "${hash}" ]]; then
        echo "Error: no checksum for ${filename} in CGW sha256sum.txt for v${version}" >&2
        exit 1
    fi
    printf '%s' "${hash}"
}

EXPECTED=""
if [[ -n "${LOCKFILE}" && -f "${LOCKFILE}" ]]; then
    EXPECTED="$(expected_from_lockfile "${LOCKFILE}" "${FILENAME}")"
fi

if [[ -z "${EXPECTED}" ]]; then
    if [[ -z "${HELM_VERSION}" ]]; then
        echo "Error: no pinned checksum for ${FILENAME} and HELM_VERSION not provided" >&2
        exit 1
    fi
    echo "No lockfile checksum for ${FILENAME}; verifying against CGW sha256sum.txt..."
    EXPECTED="$(expected_from_mirror "${HELM_VERSION#v}" "${FILENAME}")"
fi

ACTUAL="$(file_sha256 "${ARCHIVE}")"
if [[ "${ACTUAL}" != "${EXPECTED}" ]]; then
    echo "Error: checksum mismatch for ${FILENAME}" >&2
    echo "  expected: ${EXPECTED}" >&2
    echo "  actual:   ${ACTUAL}" >&2
    exit 1
fi

echo "Verified ${FILENAME} sha256=${ACTUAL}"
