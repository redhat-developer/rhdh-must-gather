#!/bin/bash
# Validate Helm deployment collection in must-gather output
#
# Usage:
#   ./tests/e2e/test-helm.sh --validate --output-dir <dir> --namespace <ns> --release <name> [--replicas <n>]
#
# Options:
#   --output-dir <dir>  Path to must-gather output directory (required)
#   --namespace <ns>    Namespace where release was deployed (required)
#   --release <name>    Name of the Helm release (required)
#   --replicas <n>      Expected number of replicas (default: 1)
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
REPLICAS=1

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
        --replicas)
            REPLICAS="$2"
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
log_info "Expected replicas: $REPLICAS"

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
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/pods" "all pod data in Helm collection directory"

# Validate per-pod logs structure
DEPLOY_DIR="$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment"
if [ -d "$DEPLOY_DIR/logs" ]; then
    log_pod_count=$(find "$DEPLOY_DIR/logs" -mindepth 1 -maxdepth 1 -type d -name 'pod=*' 2>/dev/null | wc -l)
    if [ "$log_pod_count" -ge 1 ]; then
        log_info "✓ Found $log_pod_count pod log directory(ies)"
        for pod_log_dir in "$DEPLOY_DIR/logs"/pod=*; do
            if [ -d "$pod_log_dir" ]; then
                pod_name=$(basename "$pod_log_dir")
                # Check for at least one container log directory
                container_count=$(find "$pod_log_dir" -mindepth 1 -maxdepth 1 -type d -name 'container=*' 2>/dev/null | wc -l)
                if [ "$container_count" -ge 1 ]; then
                    log_info "✓ Found $container_count container log directory(ies) in $pod_name"
                else
                    log_error "✗ No container log directories in $pod_name"
                    ((ERRORS++))
                fi
            fi
        done
    else
        log_error "✗ No pod log directories found in $DEPLOY_DIR/logs"
        ((ERRORS++))
    fi
else
    log_error "✗ logs directory not found in $DEPLOY_DIR"
    ((ERRORS++))
fi

# Validate expected number of pods
PODS_FILE="$OUTPUT_DIR/helm/releases/ns=$NAMESPACE/$RELEASE_NAME/deployment/pods/pods.txt"
if [ -f "$PODS_FILE" ]; then
    # Count pods by counting lines that start with the release name (pod names)
    POD_COUNT=$(grep -c "^$RELEASE_NAME" "$PODS_FILE" 2>/dev/null || echo 0)
    if [ "$POD_COUNT" -eq "$REPLICAS" ]; then
        log_info "✓ Found expected $REPLICAS pod(s) in pods.txt"
    else
        log_error "✗ Expected $REPLICAS pod(s), found $POD_COUNT in pods.txt"
        ((ERRORS++))
    fi
else
    log_error "✗ pods.txt not found"
    ((ERRORS++))
fi

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
