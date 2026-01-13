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
        ((ERRORS++))
    fi
}

check_file_valid_json() {
    local file="$1"
    local description="$2"
    check_file_exists "$file" "$description"
    if ! jq . "$file" >/dev/null 2>&1; then
        log_error "✗ $description is not valid JSON: $file"
        ((ERRORS++))
    fi
}

check_dir_not_empty() {
    local dir="$1"
    local description="$2"
    check_dir_exists "$dir" "$description"
    if [ -n "$(ls -A "$dir")" ]; then
        log_info "✓ Found non-empty $description: $dir"
    else
        log_error "✗ $description is empty"
        ((ERRORS++))
    fi
}

check_file_contains() {
    local file="$1"
    local content="$2"
    local description="$3"
    check_file_exists "$file" "$description"
    if grep -q "$content" "$file"; then
        log_info "✓ Found $content in $file"
    else
        log_error "✗ $description does not contain '$content': $file"
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
