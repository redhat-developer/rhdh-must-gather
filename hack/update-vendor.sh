#!/usr/bin/env bash
#
# Sync a vendored Git subtree and prune it to only build-essential files.
#
# Usage:
#   hack/update-vendor.sh <name> <version>
#
# Examples:
#   hack/update-vendor.sh websocat v1.14.1
#
# The script will:
#   1. Add or update the Git subtree under vendor/<name>
#   2. Remove non-essential files (docs, CI, tests, examples)
#   3. Update the version comment in the Containerfile
#
# Changes are left unstaged for the caller to review and commit.
#

set -euo pipefail

declare -A REPOS=(
    [websocat]="https://github.com/vi/websocat.git"
)

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
REPO="${REPOS[$NAME]:-}"

if [[ -z "$REPO" ]]; then
    echo "Error: unknown vendor '${NAME}'. Supported: ${!REPOS[*]}"
    exit 1
fi

if ! command -v git &>/dev/null; then
    echo "Error: git is required"
    exit 1
fi

# Ensure we're at the repo root
cd "$(git rev-parse --show-toplevel)"

echo "Syncing ${NAME} ${VERSION} from ${REPO}..."

if [[ -d "${PREFIX}" ]]; then
    git subtree pull --prefix="${PREFIX}" "${REPO}" "${VERSION}" --squash \
        -m "chore(vendor): update ${NAME} to ${VERSION}"
else
    git subtree add --prefix="${PREFIX}" "${REPO}" "${VERSION}" --squash
fi

echo "Pruning non-essential files from ${PREFIX}..."

prune_websocat() {
    local dir="$1"

    # Remove non-essential top-level directories
    local -a remove_dirs=(.github misc tests)
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
    websocat) prune_websocat "$PREFIX" ;;
esac

# Remove any empty directories left behind
find "$PREFIX" -type d -empty -delete

# Update the version comment in the Containerfile
sed -i "s|# ${NAME} v[^ ]* —|# ${NAME} ${VERSION} —|" Containerfile

echo "Remaining files:"
find "$PREFIX" -type f | sort

echo ""
echo "Done. Review changes with 'git status' and commit when ready."
