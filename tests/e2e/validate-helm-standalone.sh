#!/bin/bash
# Validate standalone Helm deployment collection in must-gather output
# (Deployments created via `helm template | kubectl apply`, not tracked by Helm releases)
#
# Usage:
#   ./tests/e2e/test-helm-standalone.sh --validate --output-dir <dir> --namespace <ns> --deployment <name> [--postgres <name>]
#
# Options:
#   --output-dir <dir>   Path to must-gather output directory (required)
#   --namespace <ns>     Namespace where deployment was created (required)
#   --deployment <name>  Name of the RHDH deployment (required)
#   --postgres <name>    Name of the PostgreSQL StatefulSet (optional)
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
DEPLOYMENT_NAME=""
POSTGRES_NAME=""

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
        --deployment)
            DEPLOYMENT_NAME="$2"
            shift 2
            ;;
        --postgres)
            POSTGRES_NAME="$2"
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [ "$MODE" != "validate" ]; then
    log_error "Usage: $0 --validate --output-dir <dir> --namespace <ns> --deployment <name> [--postgres <name>]"
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
if [ -z "$DEPLOYMENT_NAME" ]; then
    log_error "--deployment is required"
    exit 1
fi

log_info ""
log_info "=========================================="
log_info "Validating standalone Helm deployment collection"
log_info "=========================================="
log_info "Output directory: $OUTPUT_DIR"
log_info "Namespace: $NAMESPACE"
log_info "Deployment: $DEPLOYMENT_NAME"
if [ -n "$POSTGRES_NAME" ]; then
    log_info "PostgreSQL StatefulSet: $POSTGRES_NAME"
fi

reset_errors

check_dir_not_empty "$OUTPUT_DIR/helm/standalone" "standalone Helm deployments directory"
check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE" "$NAMESPACE namespace in standalone directory"
check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME" "$DEPLOYMENT_NAME in standalone directory"
check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/standalone-note.txt" "standalone deployment note"
check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/helm-metadata.txt" "Helm metadata for standalone deployment"
check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment.yaml" "deployment YAML for standalone deployment"
check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment.describe.txt" "deployment description for standalone deployment"

# Verify the standalone deployment is listed in the all-rhdh-releases.txt with (standalone) marker
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "(standalone)" "standalone marker in releases list"
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "$NAMESPACE" "standalone namespace in releases list"

# Verify that standalone deployments have the deployment data collected
check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment" "deployment data in standalone directory"
check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/logs-app.txt" "logs for standalone deployment"
check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/pods" "pod data for standalone deployment"

# Verify process collection from the running standalone deployment
# Unlike the native Helm deployment (which is in CreateContainerConfigError), the standalone deployment is running
check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/processes" "processes directory for running standalone deployment"

# Find the pod directory and verify process files exist
standalone_pod_dirs=$(find "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/processes" -mindepth 1 -maxdepth 1 -type d -name 'pod=*' 2>/dev/null | wc -l)
if [ "$standalone_pod_dirs" -ge 1 ]; then
    log_info "✓ Found $standalone_pod_dirs pod process directory(ies) for standalone deployment"
else
    log_error "✗ Expected at least 1 pod process directory for standalone deployment, found $standalone_pod_dirs"
    ((ERRORS++))
fi

# Validate process files in each pod directory
for pod_dir in "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/processes"/pod=*; do
    if [ -d "$pod_dir" ]; then
        pod_name=$(basename "$pod_dir")
        check_file_not_empty "$pod_dir/container=backstage-backend.txt" "backstage-backend container process list in standalone $pod_name"
        check_file_contains "$pod_dir/container=backstage-backend.txt" "PID" "process list header in standalone $pod_name"
        check_file_contains "$pod_dir/container=backstage-backend.txt" "node" "Node.js process in process list in standalone $pod_name"
    fi
done

# Verify dependent services (PostgreSQL) are collected for standalone deployments
log_info ""
log_info "--- Validating dependent services collection for standalone deployment ---"
if [ -n "$POSTGRES_NAME" ]; then
    check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/dependencies" "dependencies directory for standalone deployment"
    check_dir_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/dependencies/$POSTGRES_NAME" "PostgreSQL dependency in standalone deployment"
    check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/dependencies/$POSTGRES_NAME/statefulset.yaml" "PostgreSQL StatefulSet YAML"
    check_file_not_empty "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/dependencies/$POSTGRES_NAME/statefulset.describe.txt" "PostgreSQL StatefulSet description"
    # Verify PostgreSQL logs are collected
    postgres_log_files=$(find "$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/dependencies/$POSTGRES_NAME" -name 'logs-*.txt' 2>/dev/null | wc -l)
    if [ "$postgres_log_files" -ge 1 ]; then
        log_info "✓ Found $postgres_log_files PostgreSQL log file(s)"
    else
        log_error "✗ Expected at least 1 PostgreSQL log file, found $postgres_log_files"
        ((ERRORS++))
    fi
else
    log_warn "○ Skipping dependent services validation (PostgreSQL not found in this chart version)"
fi

# Verify the standalone deployment is NOT in the native releases directory (it should only be in standalone/)
if [ -d "$OUTPUT_DIR/helm/releases/ns=$NAMESPACE" ]; then
    log_error "✗ Standalone deployment namespace should NOT be in helm/releases/ (should only be in helm/standalone/)"
    ((ERRORS++))
else
    log_info "✓ Correctly: standalone deployment is not in helm/releases/ directory"
fi

log_info ""
log_info "Standalone Helm validation completed with $(get_error_count) error(s)"

if [ "$(get_error_count)" -eq 0 ]; then
    exit 0
else
    exit 1
fi
