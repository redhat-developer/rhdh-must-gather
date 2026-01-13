#!/bin/bash
# Validate Helm deployment collection in must-gather output
#
# Usage:
#   ./tests/e2e/test-helm.sh --validate --output-dir <dir> --namespace <ns> --release <name>
#
# Options:
#   --output-dir <dir>  Path to must-gather output directory (required)
#   --namespace <ns>    Namespace where release was deployed (required)
#   --release <name>    Name of the Helm release (required)
#
# Exit codes:
#   0 - All validations passed
#   1 - One or more validations failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tests/e2e/lib/test-utils.sh
source "$SCRIPT_DIR/lib/test-utils.sh"

# Default values
MODE=""
NAMESPACE=""
OUTPUT_DIR=""
RELEASE_NAME=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --validate)
            MODE="validate"
            shift
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --release)
            RELEASE_NAME="$2"
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [ "$MODE" != "validate" ]; then
    log_error "Usage: $0 --validate --output-dir <dir> --namespace <ns> --release <name>"
    exit 1
fi

if [ -z "$OUTPUT_DIR" ]; then
    log_error "--output-dir is required"
    exit 1
fi
if [ -z "$NAMESPACE" ]; then
    log_error "--namespace is required"
    exit 1
fi
if [ -z "$RELEASE_NAME" ]; then
    log_error "--release is required"
    exit 1
fi

log_info ""
log_info "=========================================="
log_info "Validating Helm deployment collection"
log_info "=========================================="
log_info "Output directory: $OUTPUT_DIR"
log_info "Namespace: $NAMESPACE"
log_info "Helm release: $RELEASE_NAME"

reset_errors

check_dir_not_empty "$OUTPUT_DIR/helm" "Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "release info text"
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "$RELEASE_NAME" "$RELEASE_NAME is listed in the Helm releases list"
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "$NAMESPACE" "$NAMESPACE is displayed in the Helm releases list"

check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE" "$NAMESPACE namespace in Helm collection directory"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/_configmaps" "$NAMESPACE namespace configmaps in Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/values.yaml" "values.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/all-values.yaml" "all-values.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/manifest.yaml" "manifest.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/hooks.yaml" "hooks.yaml"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment" "all deployment data in Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/logs-app.txt" "deployment logs"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/pods" "all pod data in Helm collection directory"

# Processes are only collected from running pods; the Helm deployment is intentionally misconfigured (CreateContainerConfigError)
# so the processes directory should NOT exist (or be empty if created)
if [ -d "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/processes" ]; then
    # If processes dir exists, it should be empty (no running pods to collect from)
    if [ -n "$(ls -A "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/processes" 2>/dev/null)" ]; then
        log_error "✗ Unexpectedly found process data in processes directory (expected empty - no running pods)"
        ((ERRORS++))
    else
        log_info "✓ Correctly found empty processes directory (no running pods)"
    fi
else
    log_info "✓ Correctly missing processes directory (expected - no running pods)"
fi

log_info ""
log_info "Helm validation completed with $(get_error_count) error(s)"

if [ "$(get_error_count)" -eq 0 ]; then
    exit 0
else
    exit 1
fi
