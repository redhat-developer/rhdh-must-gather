#!/bin/bash
# Validate common/general output in must-gather output
# This includes version info, platform info, sanitization report, and namespace-inspect data.
#
# Usage:
#   ./tests/e2e/validate-common.sh <output_dir> [--local] [--namespaces ns1,ns2,...]
#
# Arguments:
#   output_dir    - Path to the must-gather output directory
#   --local       - Running in local mode (skips must-gather.log check)
#   --namespaces  - Comma-separated list of namespaces to validate in namespace-inspect
#
# Exit codes:
#   0 - All validations passed
#   1 - One or more validations failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tests/e2e/lib/test-utils.sh
source "$SCRIPT_DIR/lib/test-utils.sh"

if [ $# -lt 1 ]; then
    log_error "Usage: $0 <output_dir> [--local] [--namespaces ns1,ns2,...]"
    exit 1
fi

OUTPUT_DIR="$1"
shift

LOCAL_MODE=false
NAMESPACES=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --local)
            LOCAL_MODE=true
            shift
            ;;
        --namespaces)
            NAMESPACES="$2"
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

log_info ""
log_info "=========================================="
log_info "Validating common must-gather output"
log_info "=========================================="
log_info "Output directory: $OUTPUT_DIR"
log_info "Local mode: $LOCAL_MODE"
if [ -n "$NAMESPACES" ]; then
    log_info "Namespaces to validate: $NAMESPACES"
fi

reset_errors

# Check must-gather.log (only when not in local mode)
if [ "$LOCAL_MODE" = true ]; then
    log_info "○ Skipping must-gather.log check in local mode (logs go to console)"
else
    check_file_not_empty "$OUTPUT_DIR/must-gather.log" "must-gather container logs"
fi

# Check version file
check_file_not_empty "$OUTPUT_DIR/version" "version file"

# Check sanitization report
check_file_not_empty "$OUTPUT_DIR/sanitization-report.txt" "sanitization report"

# Check platform information
check_file_not_empty "$OUTPUT_DIR/platform/platform.txt" "platform information file (text)"
check_file_not_empty "$OUTPUT_DIR/platform/platform.json" "platform information file (JSON)"
check_file_valid_json "$OUTPUT_DIR/platform/platform.json" "platform information file (JSON)"

PLT=$(jq -r '.platform' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$PLT" ]; then
    log_error "✗ platform is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
else
    log_info "✓ Platform: $PLT"
fi

UNDERLYING_PLT=$(jq -r '.underlying' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$UNDERLYING_PLT" ]; then
    log_error "✗ 'underlying' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
else
    log_info "✓ Underlying platform: $UNDERLYING_PLT"
fi

K8S_VER=$(jq -r '.k8sVersion' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$K8S_VER" ]; then
    log_error "✗ 'k8sVersion' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
else
    log_info "✓ Kubernetes version: $K8S_VER"
fi

# Check OpenShift-specific fields if running on OpenShift
if is_openshift; then
    OCP_VER=$(jq -r '.ocpVersion' "$OUTPUT_DIR/platform/platform.json")
    if [ -z "$OCP_VER" ]; then
        log_error "✗ 'ocpVersion' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
        ((ERRORS++))
    else
        log_info "✓ OpenShift version: $OCP_VER"
    fi
fi

# Check namespace-inspect directory
check_dir_not_empty "$OUTPUT_DIR/namespace-inspect" "namespace-inspect directory"

# Validate specific namespaces if provided
if [ -n "$NAMESPACES" ]; then
    IFS=',' read -ra NS_ARRAY <<< "$NAMESPACES"
    for ns in "${NS_ARRAY[@]}"; do
        check_dir_not_empty "$OUTPUT_DIR/namespace-inspect/namespaces/$ns" "$ns in namespace-inspect directory"
    done
fi

# Optional: Check cluster-info (collection is opt-in)
if [ -d "$OUTPUT_DIR/cluster-info" ]; then
    log_info "✓ Found cluster info data directory"
    check_dir_not_empty "$OUTPUT_DIR/cluster-info" "cluster info data directory"
else
    log_warn "○ Cluster info not present (expected - collection is opt-in)"
fi

log_info ""
log_info "Common validation completed with $(get_error_count) error(s)"

if [ "$(get_error_count)" -eq 0 ]; then
    exit 0
else
    exit 1
fi
