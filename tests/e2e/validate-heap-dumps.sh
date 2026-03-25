#!/bin/bash
# Validate heap dump collection in must-gather output
#
# Usage:
#   ./tests/e2e/validate-heap-dumps.sh --validate --output-dir <dir> --namespace <ns> --deployment <name>
#
# Options:
#   --output-dir <dir>   Path to must-gather output directory (required)
#   --namespace <ns>     Namespace where deployment was created (required)
#   --deployment <name>  Name of the RHDH deployment (required)
#   --type <type>        Deployment type: "standalone" or "operator" (default: standalone)
#   --cr <name>          Backstage CR name (required if type=operator)
#   --require-success    Fail validation if heap dump collection failed (instead of just warning)
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
DEPLOYMENT_TYPE="standalone"
CR_NAME=""
REQUIRE_SUCCESS=false

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
        --type)
            DEPLOYMENT_TYPE="$2"
            shift 2
            ;;
        --cr)
            CR_NAME="$2"
            shift 2
            ;;
        --require-success)
            REQUIRE_SUCCESS=true
            shift
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [ "$MODE" != "validate" ]; then
    log_error "Usage: $0 --validate --output-dir <dir> --namespace <ns> --deployment <name> [--type standalone|operator] [--cr <name>] [--require-success]"
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
if [ "$DEPLOYMENT_TYPE" = "operator" ] && [ -z "$CR_NAME" ]; then
    log_error "--cr is required when --type=operator"
    exit 1
fi

log_info ""
log_info "=========================================="
log_info "Validating heap dump collection"
log_info "=========================================="
log_info "Output directory: $OUTPUT_DIR"
log_info "Namespace: $NAMESPACE"
log_info "Deployment: $DEPLOYMENT_NAME"
log_info "Type: $DEPLOYMENT_TYPE"
if [ -n "$CR_NAME" ]; then
    log_info "Backstage CR: $CR_NAME"
fi

reset_errors

# Determine the heap-dumps directory based on deployment type
if [ "$DEPLOYMENT_TYPE" = "standalone" ]; then
    HEAP_DUMPS_BASE="$OUTPUT_DIR/helm/standalone/ns=$NAMESPACE/$DEPLOYMENT_NAME/deployment/heap-dumps"
elif [ "$DEPLOYMENT_TYPE" = "operator" ]; then
    HEAP_DUMPS_BASE="$OUTPUT_DIR/operator/backstage-crs/ns=$NAMESPACE/$CR_NAME/deployment/heap-dumps"
else
    log_error "Unknown deployment type: $DEPLOYMENT_TYPE"
    exit 1
fi

log_info "Looking for heap dumps in: $HEAP_DUMPS_BASE"

# Check that heap-dumps directory exists
check_dir_exists "$HEAP_DUMPS_BASE" "heap-dumps directory"

# Find pod directories
pod_dirs=$(find "$HEAP_DUMPS_BASE" -mindepth 1 -maxdepth 1 -type d -name 'pod=*' 2>/dev/null || true)
pod_count=$(echo "$pod_dirs" | grep -c 'pod=' || echo 0)

if [ "$pod_count" -ge 1 ]; then
    log_info "Found $pod_count pod directory(ies) with heap dump data"
else
    log_error "No pod directories found in heap-dumps directory"
    _dump_dir_context "$HEAP_DUMPS_BASE"
    ((ERRORS++))
fi

# Validate each pod's heap dump collection
for pod_dir in $pod_dirs; do
    if [ -d "$pod_dir" ]; then
        pod_name=$(basename "$pod_dir")
        log_info ""
        log_info "--- Validating heap dumps for $pod_name ---"

        # Find container directories
        container_dirs=$(find "$pod_dir" -mindepth 1 -maxdepth 1 -type d -name 'container=*' 2>/dev/null || true)

        for container_dir in $container_dirs; do
            if [ -d "$container_dir" ]; then
                container_name=$(basename "$container_dir")
                log_info "Checking $container_name..."

                # Check for heap dump log (always present)
                check_file_exists "$container_dir/heap-dump.log" "heap dump log for $container_name"

                # Check for process info (always present when collection was attempted)
                check_file_exists "$container_dir/process-info.txt" "process info for $container_name"

                # Check for heap snapshot file OR collection-failed.txt
                # (One of these should exist - either collection succeeded or we have failure info)
                heap_snapshot=$(find "$container_dir" -maxdepth 1 -name '*.heapsnapshot' -type f 2>/dev/null | head -1)
                collection_failed="$container_dir/collection-failed.txt"

                if [ -n "$heap_snapshot" ] && [ -f "$heap_snapshot" ]; then
                    log_info "Found heap snapshot: $(basename "$heap_snapshot")"
                    # Verify the heap snapshot is not empty and has reasonable size
                    snapshot_size=$(stat -c%s "$heap_snapshot" 2>/dev/null || echo 0)
                    if [ "$snapshot_size" -gt 1000 ]; then
                        log_info "Heap snapshot size: $snapshot_size bytes"
                    else
                        log_error "Heap snapshot is too small ($snapshot_size bytes), may be corrupted"
                        ((ERRORS++))
                    fi
                elif [ -f "$collection_failed" ]; then
                    # Collection failed but we have diagnostic info
                    if [ "$REQUIRE_SUCCESS" = true ]; then
                        # In strict mode, this is an error
                        log_error "Heap dump collection failed (see $collection_failed)"
                        log_error "Use --require-success=false to allow collection failures"
                        ((ERRORS++))
                    else
                        # In lenient mode, just warn (collection might fail due to Node.js configuration)
                        log_warn "Heap dump collection failed (see $collection_failed)"
                        log_warn "This may be expected if Node.js inspector couldn't be activated"
                    fi
                    # Check that the failure file has useful content
                    if [ -s "$collection_failed" ]; then
                        log_info "collection-failed.txt contains diagnostic information"
                    else
                        log_error "collection-failed.txt is empty (should contain troubleshooting info)"
                        ((ERRORS++))
                    fi
                else
                    log_error "Neither heap snapshot nor collection-failed.txt found for $container_name"
                    _dump_dir_context "$container_dir"
                    ((ERRORS++))
                fi
            fi
        done
    fi
done

log_info ""
log_info "Heap dump validation completed with $(get_error_count) error(s)"

if [ "$(get_error_count)" -eq 0 ]; then
    exit 0
else
    exit 1
fi
