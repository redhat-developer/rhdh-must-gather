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

# Returns the current error count
get_error_count() {
    echo "$ERRORS"
}

# Resets the error counter
reset_errors() {
    ERRORS=0
}
