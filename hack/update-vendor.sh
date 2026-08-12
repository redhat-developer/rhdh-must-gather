#!/usr/bin/env bash
#
# Sync a vendored Git subtree and prune it to only build-essential files.
#
# Usage:
#   hack/update-vendor.sh <name> <version>
#
# Examples:
#   hack/update-vendor.sh websocat v1.14.1
#   hack/update-vendor.sh helm v4.2.3
#
# Helm is ideally installed from CGW binaries (see hack/update-helm-lockfile.sh), but when a newer
# version exists for which binaries don't exist, switch to using vendored sources.
# Vendor helm source when the CGW mirror does not publish the requested version.
#
# The script will:
#   1. Clone the upstream tag into a temp directory and copy to vendor/<name>
#   2. Remove non-essential files (docs, CI, tests, examples)
#   3. Update the version in the Containerfile and Makefile
#
# Changes are left unstaged for the caller to review and commit.
#

set -euo pipefail

usage() {
    sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
    exit 1
}

if [[ $# -ne 2 ]]; then
    usage
fi

NAME="$1"
VERSION="$2"
PREFIX="vendor/${NAME}"

case "$NAME" in
    helm)     REPO="https://github.com/helm/helm.git" ;;
    websocat) REPO="https://github.com/vi/websocat.git" ;;
    *)
        echo "Error: unknown vendor '${NAME}'. Supported: helm, websocat"
        echo "For Helm CGW binaries, use: hack/update-helm-lockfile.sh <version>"
        exit 1
        ;;
esac

if ! command -v git &>/dev/null; then
    echo "Error: git is required"
    exit 1
fi

# Ensure we're at the repo root
cd "$(git rev-parse --show-toplevel)"

echo "Syncing ${NAME} ${VERSION} from ${REPO}..."

# Clone the tag into a temp directory and copy to vendor/<name>.
# This avoids git subtree entirely — no merge conflicts from pruned files,
# no commits created during sync, no clean-tree requirement.
TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

git clone --depth=1 --branch="${VERSION}" "${REPO}" "${TMPDIR}/${NAME}"
rm -rf "${PREFIX}"
mkdir -p "${PREFIX}"
cp -pPR "${TMPDIR}/${NAME}/." "${PREFIX}/"
rm -rf "${PREFIX}/.git"

echo "Pruning non-essential files from ${PREFIX}..."

prune_helm() {
    local dir="$1"

    # Remove non-essential top-level directories
    local remove_dirs=(.github scripts testdata)
    for d in "${remove_dirs[@]}"; do
        rm -rf "${dir:?}/${d}"
    done

    # Remove non-essential top-level files (keep go.mod, go.sum, LICENSE*)
    find "$dir" -maxdepth 1 -type f \
        ! -name 'go.mod' \
        ! -name 'go.sum' \
        ! -name 'LICENSE*' \
        -delete

    # Remove test files and nested testdata (go build ignores them)
    find "$dir" -name '*_test.go' -delete
    find "$dir" -type d -name testdata -exec rm -rf {} +
}

finalize_helm() {
    local dir="$1"

    if ! command -v go &>/dev/null; then
        echo "Error: go is required to vendor Helm module dependencies"
        exit 1
    fi

    # Drop test-only requires and vendor external modules for hermetic (cachi2) builds.
    (cd "$dir" && go mod tidy && go mod vendor)

    # Verify the pruned tree still builds offline with vendored deps.
    (cd "$dir" && CGO_ENABLED=0 go build -mod=vendor -trimpath -o /dev/null ./cmd/helm)
}

prune_websocat() {
    local dir="$1"

    # Remove non-essential top-level directories
    local remove_dirs=(.github misc tests)
    for d in "${remove_dirs[@]}"; do
        rm -rf "${dir:?}/${d}"
    done

    # Remove non-essential top-level files (keep Cargo.toml, Cargo.lock, LICENSE*)
    find "$dir" -maxdepth 1 -type f \
        ! -name 'Cargo.toml' \
        ! -name 'Cargo.lock' \
        ! -name 'LICENSE*' \
        -delete
}

case "$NAME" in
    helm)
        prune_helm "$PREFIX"
        finalize_helm "$PREFIX"
        ;;
    websocat) prune_websocat "$PREFIX" ;;
esac

# Remove any empty directories left behind
find "$PREFIX" -type d -empty -delete

# Update the version comment in both Containerfiles
sed -i.bak "s|# ${NAME} v[^ ]* —|# ${NAME} ${VERSION} —|" Containerfile && rm -f Containerfile.bak
sed -i.bak "s|# ${NAME} v[^ ]* —|# ${NAME} ${VERSION} —|" .rhdh/docker/Containerfile && rm -f .rhdh/docker/Containerfile.bak

# Update the version variable in the Makefile (e.g., WEBSOCAT_VERSION := 1.14.0)
MAKE_VAR="$(echo "${NAME}" | tr '[:lower:]' '[:upper:]')_VERSION"
VERSION_NUM="${VERSION#v}"
sed -i.bak "s|^${MAKE_VAR} := .*|${MAKE_VAR} := ${VERSION_NUM}|" Makefile && rm -f Makefile.bak

echo "Remaining files:"
find "$PREFIX" -type f | sort

echo ""
echo "Done. Review changes with 'git status' and commit when ready."
