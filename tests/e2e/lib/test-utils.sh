#!/bin/bash
# Common utilities for E2E tests
# This file is sourced by all E2E test scripts.

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Global error counter - each script should reset this before running checks
ERRORS=0

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Dump file content to stderr for debugging failed assertions.
# Truncates to first and last 50 lines if the file is large.
_dump_file_context() {
    local file="$1"
    if [ ! -e "$file" ]; then
        log_error "  ↳ File does not exist: $file"
        local parent
        parent="$(dirname "$file")"
        if [ -d "$parent" ]; then
            log_error "  ↳ Parent directory contents ($(basename "$parent")/):"
            # shellcheck disable=SC2012
            ls -la "$parent" | while IFS= read -r line; do
                log_error "      $line"
            done
        else
            log_error "  ↳ Parent directory also missing: $parent"
        fi
        return
    fi
    if [ ! -s "$file" ]; then
        log_error "  ↳ File exists but is empty (0 bytes): $file"
        return
    fi
    local total_lines
    total_lines=$(wc -l < "$file")
    local max_lines=100
    log_error "  ↳ File content ($total_lines lines):"
    if [ "$total_lines" -le "$max_lines" ]; then
        while IFS= read -r line; do
            log_error "      $line"
        done < "$file"
    else
        log_error "      --- first 50 lines ---"
        head -n 50 "$file" | while IFS= read -r line; do
            log_error "      $line"
        done
        log_error "      --- ... truncated $(( total_lines - 100 )) lines ... ---"
        log_error "      --- last 50 lines ---"
        tail -n 50 "$file" | while IFS= read -r line; do
            log_error "      $line"
        done
    fi
}

# Dump directory listing for debugging failed assertions.
_dump_dir_context() {
    local dir="$1"
    if [ ! -e "$dir" ]; then
        log_error "  ↳ Directory does not exist: $dir"
        local parent
        parent="$(dirname "$dir")"
        if [ -d "$parent" ]; then
            log_error "  ↳ Parent directory contents ($(basename "$parent")/):"
            # shellcheck disable=SC2012
            ls -la "$parent" | while IFS= read -r line; do
                log_error "      $line"
            done
        else
            log_error "  ↳ Parent directory also missing: $parent"
        fi
        return
    fi
    log_error "  ↳ Directory listing:"
    # shellcheck disable=SC2012
    ls -la "$dir" | while IFS= read -r line; do
        log_error "      $line"
    done
}

# Detect if we're running on an OpenShift cluster
is_openshift() {
    # Check if the cluster has OpenShift-specific API resources
    kubectl api-resources --api-group=config.openshift.io 2>/dev/null | grep -q clusterversion
}

# Validation helper functions
check_file_exists() {
    local file="$1"
    local description="$2"
    if [ -f "$file" ]; then
        log_info "✓ Found $description: $file"
    else
        log_error "✗ Missing $description: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_dir_exists() {
    local dir="$1"
    local description="$2"
    if [ -d "$dir" ]; then
        log_info "✓ Found $description: $dir"
    else
        log_error "✗ Missing $description: $dir"
        _dump_dir_context "$dir"
        ((ERRORS++))
    fi
}

check_file_not_empty() {
    local file="$1"
    local description="$2"
    check_file_exists "$file" "$description"
    if [ -s "$file" ]; then
        log_info "✓ Found non-empty $description: $file"
    else
        log_error "✗ $description is empty: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_file_valid_json() {
    local file="$1"
    local description="$2"
    check_file_exists "$file" "$description"
    if ! jq . "$file" >/dev/null 2>&1; then
        log_error "✗ $description is not valid JSON: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_dir_not_empty() {
    local dir="$1"
    local description="$2"
    check_dir_exists "$dir" "$description"
    if [ -d "$dir" ] && [ -n "$(ls -A "$dir" 2>/dev/null)" ]; then
        log_info "✓ Found non-empty $description: $dir"
    else
        log_error "✗ $description is empty"
        _dump_dir_context "$dir"
        ((ERRORS++))
    fi
}

check_file_contains() {
    local file="$1"
    local content="$2"
    local description="$3"
    check_file_exists "$file" "$description"
    if [ -f "$file" ] && grep -q "$content" "$file"; then
        log_info "✓ Found $content in $file"
    else
        log_error "✗ $description does not contain '$content': $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_file_no_error() {
    local file="$1"
    local description="$2"
    if [ -f "$file" ] && grep -qiE 'Error from server|NotFound|forbidden|unknown command|Command failed or timed out' "$file"; then
        log_error "✗ $description contains error output: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

# Returns the current error count
get_error_count() {
    echo "$ERRORS"
}

# Resets the error counter
reset_errors() {
    ERRORS=0
}

# OCI Helm chart published by RHDH midstream (Konflux CI tags: X.Y-N-CI).
HELM_CHART_OCI_REF="oci://quay.io/rhdh/chart"

# Resolve chart major.minor (X.Y) from must-gather E2E target branch.
# main: highest X.Y present on quay.io/rhdh/chart (matches rhdh CI helm::get_chart_major_version).
# release-x.y: x.y from the branch name.
chart_major_version_for_target_branch() {
    local branch="$1"
    case "$branch" in
        main)
            if ! command -v skopeo &>/dev/null; then
                log_error "skopeo is required to resolve Helm chart version for main"
                return 1
            fi
            skopeo list-tags docker://quay.io/rhdh/chart 2>/dev/null | \
                jq -r '.Tags[]' | \
                grep -oE '^[0-9]+\.[0-9]+' | \
                sort -t. -k1,1n -k2,2n -u | \
                tail -1
            ;;
        release-*)
            echo "${branch#release-}"
            ;;
        *)
            log_error "Unsupported target branch for chart resolution: $branch"
            return 1
            ;;
    esac
}

# Pick the newest X.Y-N-CI tag for major.minor X.Y from a list of tag names (stdin).
# Uses version sort so 2.0-100-CI ranks above 2.0-11-CI.
select_latest_ci_chart_version_from_tags() {
    local major="$1"
    grep -E "^${major}-[0-9]+-CI$" | sort -V | tail -1
}

# Latest Konflux CI chart tag for major.minor X.Y from quay.io/rhdh/chart.
latest_ci_chart_version_for_major() {
    local major="$1"
    if ! command -v skopeo &>/dev/null; then
        log_error "skopeo is required to resolve Helm chart version"
        return 1
    fi
    skopeo list-tags docker://quay.io/rhdh/chart 2>/dev/null | \
        jq -r '.Tags[]' | \
        select_latest_ci_chart_version_from_tags "$major"
}
