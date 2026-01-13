#!/bin/bash
# Validate Operator deployment collection in must-gather output
#
# Usage:
#   ./tests/e2e/test-operator.sh --validate --output-dir <dir> --cr <ns>:<name>:<replicas> [--cr ...]
#
# Options:
#   --output-dir <dir>              Path to must-gather output directory (required)
#   --cr <ns>:<name>:<replicas>     Backstage CR specification (can be specified multiple times)
#                                   - ns: namespace where the CR was deployed
#                                   - name: name of the Backstage CR
#                                   - replicas: expected number of replicas
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
OUTPUT_DIR=""
declare -a CR_SPECS=()

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --validate)
            MODE="validate"
            shift
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --cr)
            CR_SPECS+=("$2")
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [ "$MODE" != "validate" ]; then
    log_error "Usage: $0 --validate --output-dir <dir> --cr <ns>:<name>:<replicas> [--cr ...]"
    exit 1
fi

if [ -z "$OUTPUT_DIR" ]; then
    log_error "--output-dir is required"
    exit 1
fi

if [ ${#CR_SPECS[@]} -eq 0 ]; then
    log_error "At least one --cr specification is required"
    exit 1
fi

log_info ""
log_info "=========================================="
log_info "Validating Operator deployment collection"
log_info "=========================================="
log_info "Output directory: $OUTPUT_DIR"
log_info "Backstage CRs to validate: ${#CR_SPECS[@]}"
for spec in "${CR_SPECS[@]}"; do
    log_info "  - $spec"
done

reset_errors

# --- Validate operator collection structure ---
log_info ""
log_info "--- Validating operator collection structure ---"

check_dir_not_empty "$OUTPUT_DIR/operator" "Operator collection directory"
check_file_not_empty "$OUTPUT_DIR/operator/all-deployments.txt" "all-deployments.txt"

# --- Validate CRDs ---
log_info ""
log_info "--- Validating CRDs collection ---"
check_dir_not_empty "$OUTPUT_DIR/operator/crds" "CRDs directory"
backstage_crd=$(find "$OUTPUT_DIR/operator/crds" -name "backstages.rhdh.redhat.com.yaml" 2>/dev/null | head -1)
if [ -n "$backstage_crd" ]; then
    log_info "✓ Found Backstage CRD: $(basename "$backstage_crd")"
else
    log_error "✗ Backstage CRD not found in $OUTPUT_DIR/operator/crds"
    ((ERRORS++))
fi

# --- Validate OLM collection (if present) ---
log_info ""
log_info "--- Validating OLM collection ---"
if [ -d "$OUTPUT_DIR/operator/olm" ]; then
    # OLM directory exists - check if it has content
    if [ -n "$(ls -A "$OUTPUT_DIR/operator/olm" 2>/dev/null)" ]; then
        log_info "✓ Found non-empty OLM directory"
    else
        log_info "○ OLM directory exists but is empty (expected on vanilla Kubernetes)"
    fi
else
    log_info "○ OLM directory not present (expected on vanilla Kubernetes)"
fi

# --- Validate operator namespace collection ---
log_info ""
log_info "--- Validating operator namespace collection ---"
operator_ns_dir="$OUTPUT_DIR/operator/ns=rhdh-operator"
if [ -d "$operator_ns_dir" ]; then
    log_info "✓ Found operator namespace directory: $operator_ns_dir"
    check_dir_not_empty "$operator_ns_dir/deployments" "operator deployments directory"
    # The deployment file is named by label selector (e.g., app=rhdh-operator.yaml)
    operator_deployment=$(find "$operator_ns_dir/deployments" -name "*.yaml" 2>/dev/null | head -1)
    if [ -n "$operator_deployment" ]; then
        log_info "✓ Found operator deployment: $(basename "$operator_deployment")"
    else
        log_error "✗ No operator deployment YAML found in $operator_ns_dir/deployments"
        ((ERRORS++))
    fi
    check_file_not_empty "$operator_ns_dir/logs.txt" "operator logs"
    check_file_contains "$OUTPUT_DIR/operator/all-deployments.txt" "rhdh-operator" "operator deployment in all-deployments.txt"
else
    log_error "✗ Operator namespace directory not found at $operator_ns_dir"
    ((ERRORS++))
fi

# --- Validate each Backstage CR ---
for spec in "${CR_SPECS[@]}"; do
    IFS=':' read -r cr_namespace cr_name cr_replicas <<< "$spec"
    
    log_info ""
    log_info "--- Validating Backstage CR: $cr_name (namespace: $cr_namespace, replicas: $cr_replicas) ---"
    
    cr_base_dir="$OUTPUT_DIR/operator/backstage-crs/ns=$cr_namespace/$cr_name"
    
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs" "Backstage CRs directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$cr_namespace" "Backstage CR namespace directory"
    check_dir_not_empty "$cr_base_dir" "Backstage CR directory"
    
    # CR definition is named <cr-name>.yaml
    check_file_not_empty "$cr_base_dir/$cr_name.yaml" "Backstage CR YAML"
    check_file_not_empty "$cr_base_dir/describe.txt" "Backstage CR description"
    
    # Validate workload - the collection script uses "deployment" as the directory name
    # for both Deployment and StatefulSet workloads
    workload_dir="$cr_base_dir/deployment"
    if [ -d "$workload_dir" ]; then
        check_dir_not_empty "$workload_dir" "workload directory"
        
        # Check for deployment.yaml (used for both Deployment and StatefulSet)
        if [ -f "$workload_dir/deployment.yaml" ]; then
            log_info "✓ Found workload definition"
            check_file_not_empty "$workload_dir/deployment.yaml" "workload YAML"
            check_file_not_empty "$workload_dir/deployment.describe.txt" "workload description"
        else
            log_error "✗ No workload YAML found in $workload_dir"
            ((ERRORS++))
        fi
        
        # Validate pods
        check_dir_not_empty "$workload_dir/pods" "workload pods directory"
        
        # Count pods from pods.yaml or by checking the directory
        if [ -f "$workload_dir/pods/pods.txt" ]; then
            # Count pods from pods.txt (excluding header)
            pod_count=$(grep -c "backstage-" "$workload_dir/pods/pods.txt" 2>/dev/null || echo "0")
            if [ "$pod_count" -eq "$cr_replicas" ]; then
                log_info "✓ Found expected $cr_replicas pod(s)"
            else
                log_error "✗ Expected $cr_replicas pod(s), found $pod_count"
                ((ERRORS++))
            fi
        else
            log_warn "○ Could not verify pod count (pods.txt not found)"
        fi
        
        # Validate logs
        log_files=$(find "$workload_dir" -maxdepth 1 -name 'logs-*.txt' 2>/dev/null | wc -l)
        if [ "$log_files" -ge 1 ]; then
            log_info "✓ Found $log_files log file(s)"
        else
            log_error "✗ No log files found in workload directory"
            ((ERRORS++))
        fi
        
        # Validate processes (if pods are running)
        if [ -d "$workload_dir/processes" ]; then
            process_pod_count=$(find "$workload_dir/processes" -mindepth 1 -maxdepth 1 -type d -name 'pod=*' 2>/dev/null | wc -l)
            if [ "$process_pod_count" -ge "$cr_replicas" ]; then
                log_info "✓ Found $process_pod_count pod(s) with process information (expected: $cr_replicas)"
                # Validate process files in each pod
                for pod_dir in "$workload_dir/processes"/pod=*; do
                    if [ -d "$pod_dir" ]; then
                        pod_name=$(basename "$pod_dir")
                        check_file_not_empty "$pod_dir/container=backstage-backend.txt" "backstage-backend container process list in $pod_name"
                    fi
                done
            elif [ "$process_pod_count" -ge 1 ]; then
                log_warn "○ Found $process_pod_count pod(s) with process information, expected $cr_replicas (some pods may not be running)"
            else
                log_info "○ No pod process directories found (pods may not be running)"
            fi
        else
            log_info "○ No processes directory (pods may not be running)"
        fi
    else
        log_error "✗ Workload directory not found at $workload_dir"
        ((ERRORS++))
    fi
    
    # Validate dependent services (PostgreSQL) - stored in db-statefulset/
    deps_dir="$cr_base_dir/db-statefulset"
    if [ -d "$deps_dir" ]; then
        log_info "✓ Found PostgreSQL dependency directory"
        check_file_not_empty "$deps_dir/db-statefulset.yaml" "PostgreSQL StatefulSet YAML"
        check_file_not_empty "$deps_dir/db-statefulset.describe.txt" "PostgreSQL StatefulSet description"
        # Verify PostgreSQL logs are collected
        postgres_log_files=$(find "$deps_dir" -maxdepth 1 -name 'logs-*.txt' 2>/dev/null | wc -l)
        if [ "$postgres_log_files" -ge 1 ]; then
            log_info "✓ Found $postgres_log_files PostgreSQL log file(s)"
        else
            log_warn "○ No PostgreSQL log files found"
        fi
    else
        log_info "○ No db-statefulset directory (external database may be configured)"
    fi
done

log_info ""
log_info "Operator validation completed with $(get_error_count) error(s)"

if [ "$(get_error_count)" -eq 0 ]; then
    exit 0
else
    exit 1
fi
