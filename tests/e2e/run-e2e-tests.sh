#!/bin/bash
# E2E test script for rhdh-must-gather
# This script runs the must-gather against a Kubernetes or OpenShift cluster and validates the output.
# It automatically detects the cluster type and uses the appropriate deployment method.
#
# Usage:
#   ./tests/e2e/run-e2e-tests.sh --image <image> [OPTIONS]
#   ./tests/e2e/run-e2e-tests.sh --local [OPTIONS]
#
# Options:
#   --image <image>     Full image name (required unless --local is used)
#   --local             Run in local mode using 'make clean-out run-local' (no image required)
#   --overlay <overlay> Overlay to use (pre-built name or path). Only applicable on Kubernetes, ignored on OpenShift and local mode.
#   --target-branch <branch> Target branch (used for defaults, default: main)
#   --operator-branch <branch> Override RHDH operator branch (default: derived from --target-branch)
#   --helm-chart-version <version> Override Helm chart version (default: auto-detected from --target-branch)
#   --helm-values-file <file> Override Helm values file (default: auto-generated from --target-branch)
#   --skip-helm         Skip Helm release test
#   --skip-helm-standalone Skip standalone Helm deployment test
#   --skip-operator     Skip Operator test
#   --help              Show this help message
#
# Examples:
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123 --skip-operator
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123 --skip-helm --skip-helm-standalone
#   ./tests/e2e/run-e2e-tests.sh --local
#

set -euo pipefail
shopt -s extglob

# Get script directory for sourcing lib and calling test scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=tests/e2e/lib/test-utils.sh
source "$SCRIPT_DIR/lib/test-utils.sh"

show_help() {
    sed -n '2,/^$/p' "$0" | sed 's/^#//; s/^ //; /^$/d'
    exit 0
}

# Dump debug information to help troubleshoot CI failures
dump_debug_info() {
    log_warn "=== DEBUG INFO START ==="

    # Helm deployment info (if namespace exists)
    if [ -n "${NS_HELM:-}" ]; then
        log_warn "--- Helm Release Status (namespace: $NS_HELM) ---"
        helm -n "$NS_HELM" list 2>/dev/null || true

        if [ -n "${HELM_RELEASE:-}" ]; then
            log_warn "--- Helm Release '$HELM_RELEASE' History ---"
            helm -n "$NS_HELM" history "$HELM_RELEASE" 2>/dev/null || true
        fi

        log_warn "--- Pods in namespace $NS_HELM ---"
        kubectl -n "$NS_HELM" get pods -o wide 2>/dev/null || true

        log_warn "--- Events in namespace $NS_HELM ---"
        kubectl -n "$NS_HELM" get events --sort-by='.lastTimestamp' 2>/dev/null | tail -30 || true
    fi

    # Standalone Helm deployment info
    if [ -n "${NS_STANDALONE:-}" ]; then
        log_warn "--- Standalone Helm Deployment (namespace: $NS_STANDALONE) ---"
        log_warn "--- Pods in namespace $NS_STANDALONE ---"
        kubectl -n "$NS_STANDALONE" get pods -o wide 2>/dev/null || true

        log_warn "--- Deployments in namespace $NS_STANDALONE ---"
        kubectl -n "$NS_STANDALONE" get deployments -o wide 2>/dev/null || true

        log_warn "--- StatefulSets in namespace $NS_STANDALONE ---"
        kubectl -n "$NS_STANDALONE" get statefulsets -o wide 2>/dev/null || true

        log_warn "--- Events in namespace $NS_STANDALONE ---"
        kubectl -n "$NS_STANDALONE" get events --sort-by='.lastTimestamp' 2>/dev/null | tail -30 || true

        if [ -n "${STANDALONE_DEPLOY:-}" ]; then
            log_warn "--- Standalone Deployment '$STANDALONE_DEPLOY' ---"
            kubectl -n "$NS_STANDALONE" get deployment "$STANDALONE_DEPLOY" -o yaml 2>/dev/null || true
        fi
        if [ -n "${STANDALONE_POSTGRES:-}" ]; then
            log_warn "--- PostgreSQL StatefulSet '$STANDALONE_POSTGRES' ---"
            kubectl -n "$NS_STANDALONE" get statefulset "$STANDALONE_POSTGRES" -o yaml 2>/dev/null || true
        fi
    fi

    # Operator namespace info
    if [ -n "${NS_OPERATOR:-}" ]; then
        log_warn "--- Backstage CRs in namespace $NS_OPERATOR ---"
        kubectl -n "$NS_OPERATOR" get backstage -o wide 2>/dev/null || true

        log_warn "--- Pods in namespace $NS_OPERATOR ---"
        kubectl -n "$NS_OPERATOR" get pods -o wide 2>/dev/null || true

        log_warn "--- Events in namespace $NS_OPERATOR ---"
        kubectl -n "$NS_OPERATOR" get events --sort-by='.lastTimestamp' 2>/dev/null | tail -30 || true
    fi

    # StatefulSet namespace info
    if [ -n "${NS_STATEFULSET:-}" ]; then
        log_warn "--- Backstage CRs in namespace $NS_STATEFULSET ---"
        kubectl -n "$NS_STATEFULSET" get backstage -o wide 2>/dev/null || true

        log_warn "--- Pods in namespace $NS_STATEFULSET ---"
        kubectl -n "$NS_STATEFULSET" get pods -o wide 2>/dev/null || true

        log_warn "--- Events in namespace $NS_STATEFULSET ---"
        kubectl -n "$NS_STATEFULSET" get events --sort-by='.lastTimestamp' 2>/dev/null | tail -30 || true
    fi

    # Operator logs
    log_warn "--- RHDH Operator Deployment Status ---"
    kubectl -n rhdh-operator get deployment rhdh-operator -o wide 2>/dev/null || true
    log_warn "--- RHDH Operator Pods ---"
    kubectl -n rhdh-operator get pods -o wide 2>/dev/null || true
    log_warn "--- RHDH Operator Logs (last 100 lines) ---"
    kubectl -n rhdh-operator logs -l app.kubernetes.io/name=rhdh-operator --tail=100 2>/dev/null || true

    log_warn "=== DEBUG INFO END ==="
}

# Cleanup function to handle multiple cleanup tasks
CLEANUP_TASKS=()
# shellcheck disable=SC2329
cleanup() {
    for cmd in "${CLEANUP_TASKS[@]}"; do
        log_info "Cleanup: $cmd"
        eval "$cmd" || true
    done
}
trap cleanup EXIT

# Default values
FULL_IMAGE_NAME=""
OVERLAY=""
LOCAL_MODE=false
TARGET_BRANCH="main"
OPERATOR_BRANCH=""
HELM_CHART_VERSION=""
HELM_VALUES_FILE=""
SKIP_HELM=false
SKIP_HELM_STANDALONE=false
SKIP_OPERATOR=false

# Parse named arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --image)
            FULL_IMAGE_NAME="$2"
            shift 2
            ;;
        --local)
            LOCAL_MODE=true
            shift
            ;;
        --overlay)
            OVERLAY="$2"
            shift 2
            ;;
        --target-branch)
            TARGET_BRANCH="$2"
            shift 2
            ;;
        --operator-branch)
            OPERATOR_BRANCH="$2"
            shift 2
            ;;
        --helm-chart-version)
            HELM_CHART_VERSION="$2"
            shift 2
            ;;
        --helm-values-file)
            HELM_VALUES_FILE="$2"
            shift 2
            ;;
        --skip-helm)
            SKIP_HELM=true
            shift
            ;;
        --skip-helm-standalone)
            SKIP_HELM_STANDALONE=true
            shift
            ;;
        --skip-operator)
            SKIP_OPERATOR=true
            shift
            ;;
        --help|-h)
            show_help
            ;;
        *)
            log_error "Unknown option: $1"
            log_error "Use --help for usage information."
            exit 1
            ;;
    esac
done

if [ "$LOCAL_MODE" = true ] && [ -n "$FULL_IMAGE_NAME" ]; then
    log_warn "--image is ignored when --local is used"
fi

if [ "$LOCAL_MODE" = false ] && [ -z "$FULL_IMAGE_NAME" ]; then
    log_error "Error: --image is required (or use --local for local mode)"
    log_error "Use --help for usage information."
    exit 1
fi

if [ "$LOCAL_MODE" = true ]; then
    log_info "Starting E2E tests in local mode"
else
    log_info "Starting E2E tests with image: $FULL_IMAGE_NAME"
    if [ -n "$OVERLAY" ]; then
        log_info "Using overlay: $OVERLAY"
    fi

    # Extract registry, image name, and tag from full image name
    # e.g., quay.io/rhdh-community/rhdh-must-gather:pr-123
    REGISTRY=$(echo "$FULL_IMAGE_NAME" | cut -d'/' -f1)
    IMAGE_NAME=$(echo "$FULL_IMAGE_NAME" | cut -d':' -f1 | cut -d'/' -f2-)
    IMAGE_TAG=$(echo "$FULL_IMAGE_NAME" | cut -d':' -f2)

    log_info "Registry: $REGISTRY"
    log_info "Image name: $IMAGE_NAME"
    log_info "Image tag: $IMAGE_TAG"
fi

# Log skip options
if [ "$SKIP_HELM" = true ]; then
    log_info "Skipping Helm release test (--skip-helm)"
fi
if [ "$SKIP_HELM_STANDALONE" = true ]; then
    log_info "Skipping standalone Helm deployment test (--skip-helm-standalone)"
fi
if [ "$SKIP_OPERATOR" = true ]; then
    log_info "Skipping Operator test (--skip-operator)"
fi

# Ensure we're in the project root
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

log_info "Working directory: $PROJECT_ROOT"

# Use OPERATOR_BRANCH override if provided, otherwise use TARGET_BRANCH
EFFECTIVE_OPERATOR_BRANCH="${OPERATOR_BRANCH:-$TARGET_BRANCH}"

# Generate timestamp for namespace naming
TIMESTAMP=$(date +%s)

# Track namespaces for common validation
ALL_NAMESPACES=()

# ============================================================================
# SETUP PHASE: Deploy RHDH instances
# ============================================================================
log_info ""
log_info "=========================================="
log_info "Setting up RHDH instances for testing"
log_info "=========================================="

# --- Helm Release Setup ---
NS_HELM=""
HELM_RELEASE=""
if [ "$SKIP_HELM" = false ]; then
    NS_HELM="test-e2e-helm-$TIMESTAMP"
    log_info "Creating namespace: $NS_HELM"
    kubectl create namespace "$NS_HELM"
    CLEANUP_TASKS+=("kubectl delete namespace $NS_HELM --wait=false")
    ALL_NAMESPACES+=("$NS_HELM")

    log_info "Deploying Helm release..."
    # Use provided values file or generate one based on TARGET_BRANCH
    if [ -n "$HELM_VALUES_FILE" ]; then
        if [ ! -f "$HELM_VALUES_FILE" ]; then
            log_error "Helm values file not found: $HELM_VALUES_FILE"
            exit 1
        fi
        log_info "Using provided Helm values file: $HELM_VALUES_FILE"
        TEMP_VALUES_FILE="$HELM_VALUES_FILE"
    else
        TEMP_VALUES_FILE="$(mktemp)"
        # Generate Helm values based on TARGET_BRANCH (chart structure may differ between versions)
        case "$TARGET_BRANCH" in
            main|release-1.@(9|[1-9][0-9]))
                cat > "$TEMP_VALUES_FILE" <<EOF
route:
  enabled: false
global:
  dynamic:
    # Faster startup by disabling all default dynamic plugins
    includes: []
upstream:
  postgresql:
    # Purposely disable the local database to simulate a misconfigured application (missing external database info)
    enabled: false
EOF
                ;;
            *)
                log_error "Unsupported target branch: $TARGET_BRANCH"
                exit 1
                ;;
        esac
    fi

    HELM_RELEASE="my-helm"
    HELM_VERSION_ARGS=()
    # Determine chart version: use override if provided, otherwise auto-detect based on TARGET_BRANCH
    if [ -n "$HELM_CHART_VERSION" ]; then
        log_info "Using provided Helm chart version: $HELM_CHART_VERSION"
        HELM_VERSION_ARGS=(--version "$HELM_CHART_VERSION")
        helm -n "$NS_HELM" install "$HELM_RELEASE" oci://quay.io/rhdh/chart --values "$TEMP_VALUES_FILE" "${HELM_VERSION_ARGS[@]}"
    elif [ "$TARGET_BRANCH" != "main" ]; then
        # Extract version from branch name (e.g., release-1.9 -> 1.9)
        BRANCH_VERSION="${TARGET_BRANCH#release-}"
        log_info "Looking for Helm chart version matching ${BRANCH_VERSION}-*-CI..."
        CHART_VERSION=$(skopeo list-tags docker://quay.io/rhdh/chart 2>/dev/null | \
            jq -r '.Tags[]' | \
            grep "^${BRANCH_VERSION}-.*-CI$" | \
            sort -V | \
            tail -1)
        if [ -n "$CHART_VERSION" ]; then
            log_info "Using Helm chart version: $CHART_VERSION"
            HELM_VERSION_ARGS=(--version "$CHART_VERSION")
        else
            log_warn "No CI chart version found for ${BRANCH_VERSION}, using latest"
        fi
        helm -n "$NS_HELM" install "$HELM_RELEASE" oci://quay.io/rhdh/chart --values "$TEMP_VALUES_FILE" "${HELM_VERSION_ARGS[@]}"
    else
        # Latest upstream chart
        helm -n "$NS_HELM" install "$HELM_RELEASE" backstage \
            --repo "https://redhat-developer.github.io/rhdh-chart" \
            --values "$TEMP_VALUES_FILE"
    fi

    # Wait for the Helm-deployed RHDH pod to enter CreateContainerConfigError state (this is expected)
    log_info "Waiting for Helm-deployed RHDH pod to enter CreateContainerConfigError state (this is expected)..."
    HELM_POD=""
    TIMEOUT=120
    until HELM_POD=$(kubectl -n "$NS_HELM" get pods -l "app.kubernetes.io/instance=$HELM_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$HELM_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            break
        fi
    done
    if [ -z "$HELM_POD" ]; then
        log_error "Could not find Helm-deployed RHDH pod in namespace $NS_HELM."
        dump_debug_info
        exit 1
    fi
    if ! kubectl wait --for=jsonpath='{.status.containerStatuses[0].state.waiting.reason}=CreateContainerConfigError' pod/"$HELM_POD" -n "$NS_HELM" --timeout=5m 2>/dev/null; then
        POD_REASON=$(kubectl -n "$NS_HELM" get pod "$HELM_POD" -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}' 2>/dev/null)
        log_error "Helm-deployed pod $HELM_POD did not reach CreateContainerConfigError state (current: $POD_REASON) within expected time."
        dump_debug_info
        exit 1
    fi
    log_info "Helm release '$HELM_RELEASE' deployed successfully in namespace $NS_HELM"
else
    log_info "Skipping Helm release setup"
fi

# --- Standalone Helm Setup ---
NS_STANDALONE=""
STANDALONE_DEPLOY=""
STANDALONE_POSTGRES=""
if [ "$SKIP_HELM_STANDALONE" = false ]; then
    NS_STANDALONE="test-e2e-standalone-$TIMESTAMP"
    log_info "Creating namespace: $NS_STANDALONE"
    kubectl create namespace "$NS_STANDALONE"
    CLEANUP_TASKS+=("kubectl delete namespace $NS_STANDALONE --wait=false")
    ALL_NAMESPACES+=("$NS_STANDALONE")

    log_info "Deploying standalone Helm release (helm template + kubectl apply)..."
    STANDALONE_RELEASE="my-helm-standalone"
    STANDALONE_VALUES_FILE="$(mktemp)"
    cat > "$STANDALONE_VALUES_FILE" <<EOF
route:
  enabled: false
global:
  dynamic:
    # Faster startup by disabling all default dynamic plugins
    includes: []
EOF

    # Render the Helm chart and apply directly (no Helm release tracking)
    log_info "Rendering Helm chart with 'helm template' and applying with kubectl..."
    if [ "$TARGET_BRANCH" != "main" ]; then
        helm template "$STANDALONE_RELEASE" oci://quay.io/rhdh/chart \
            --namespace "$NS_STANDALONE" \
            --values "$STANDALONE_VALUES_FILE" \
            ${HELM_VERSION_ARGS:+"${HELM_VERSION_ARGS[@]}"} | kubectl apply -n "$NS_STANDALONE" -f -
    else
        helm template "$STANDALONE_RELEASE" backstage \
            --repo "https://redhat-developer.github.io/rhdh-chart" \
            --namespace "$NS_STANDALONE" \
            --values "$STANDALONE_VALUES_FILE" | kubectl apply -n "$NS_STANDALONE" -f -
    fi

    # Wait for the standalone-deployed RHDH pod to be running (not necessarily Ready)
    log_info "Waiting for standalone-deployed RHDH pod to be running..."
    STANDALONE_POD=""
    TIMEOUT=120
    until STANDALONE_POD=$(kubectl -n "$NS_STANDALONE" get pods -l "app.kubernetes.io/instance=$STANDALONE_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$STANDALONE_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            break
        fi
    done
    if [ -z "$STANDALONE_POD" ]; then
        log_error "Could not find standalone-deployed RHDH pod in namespace $NS_STANDALONE."
        dump_debug_info
        exit 1
    fi
    log_info "Found standalone-deployed pod: $STANDALONE_POD"
    if ! kubectl -n "$NS_STANDALONE" wait --for=jsonpath='{.status.phase}'=Running pod/"$STANDALONE_POD" --timeout=5m; then
        log_error "Standalone-deployed pod $STANDALONE_POD did not reach Running state."
        dump_debug_info
        exit 1
    fi
    log_info "Standalone-deployed pod $STANDALONE_POD is running."

    # Get the deployment name
    STANDALONE_DEPLOY=$(kubectl -n "$NS_STANDALONE" get deployment -l "app.kubernetes.io/instance=$STANDALONE_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [ -z "$STANDALONE_DEPLOY" ]; then
        log_error "Could not find standalone deployment in namespace $NS_STANDALONE."
        dump_debug_info
        exit 1
    fi

    # Get the PostgreSQL StatefulSet name (dependent service from subchart)
    STANDALONE_POSTGRES=$(kubectl -n "$NS_STANDALONE" get statefulset -l "app.kubernetes.io/managed-by=Helm,app.kubernetes.io/instance=$STANDALONE_RELEASE,app.kubernetes.io/name=postgresql" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [ -z "$STANDALONE_POSTGRES" ]; then
        log_warn "Could not find PostgreSQL StatefulSet in namespace $NS_STANDALONE (may not be part of this chart version)"
    else
        log_info "Found PostgreSQL StatefulSet: $STANDALONE_POSTGRES"
        kubectl -n "$NS_STANDALONE" wait --for=jsonpath='{.status.phase}'=Running pod/"${STANDALONE_POSTGRES}-0" --timeout=3m 2>/dev/null || true
    fi
    log_info "Standalone deployment '$STANDALONE_DEPLOY' deployed successfully in namespace $NS_STANDALONE"
else
    log_info "Skipping standalone Helm deployment setup"
fi

# --- Operator Setup ---
NS_OPERATOR=""
NS_STATEFULSET=""
BACKSTAGE_CR=""
BACKSTAGE_CR_STATEFULSET=""
if [ "$SKIP_OPERATOR" = false ]; then
    log_info "Deploying RHDH Operator from branch: $EFFECTIVE_OPERATOR_BRANCH..."
    OPERATOR_MANIFEST="https://raw.githubusercontent.com/redhat-developer/rhdh-operator/$EFFECTIVE_OPERATOR_BRANCH/dist/rhdh/install.yaml"
    kubectl apply -f "$OPERATOR_MANIFEST"
    CLEANUP_TASKS+=("kubectl delete -f $OPERATOR_MANIFEST --wait=false")

    log_info "Waiting for rhdh-operator deployment to be available in rhdh-operator namespace..."
    if ! kubectl -n rhdh-operator wait --for=condition=Available deployment/rhdh-operator --timeout=5m; then
        log_error "Timed out waiting for rhdh-operator deployment to be available."
        dump_debug_info
        exit 1
    fi
    log_info "rhdh-operator deployment is now available."
    ALL_NAMESPACES+=("rhdh-operator")

    # Create namespaces for Backstage CRs
    NS_OPERATOR="test-e2e-operator-$TIMESTAMP"
    NS_STATEFULSET="test-e2e-sts-$TIMESTAMP"
    kubectl create namespace "$NS_OPERATOR"
    kubectl create namespace "$NS_STATEFULSET"
    CLEANUP_TASKS+=("kubectl delete namespace $NS_OPERATOR --wait=false")
    CLEANUP_TASKS+=("kubectl delete namespace $NS_STATEFULSET --wait=false")
    ALL_NAMESPACES+=("$NS_OPERATOR" "$NS_STATEFULSET")

    log_info "Deploying Backstage CR (kind: Deployment in v1alpha4) with 2 replicas..."
    BACKSTAGE_CR="my-op"
    kubectl -n "$NS_OPERATOR" apply -f - <<EOF
apiVersion: rhdh.redhat.com/v1alpha4
kind: Backstage
metadata:
  name: $BACKSTAGE_CR
spec:
  deployment:
    patch:
      spec:
        replicas: 2
EOF

    log_info "Deploying Backstage CR (kind: StatefulSet in v1alpha5)..."
    BACKSTAGE_CR_STATEFULSET="my-op-statefulset"
    kubectl -n "$NS_STATEFULSET" apply -f - <<EOF
apiVersion: rhdh.redhat.com/v1alpha5
kind: Backstage
metadata:
  name: $BACKSTAGE_CR_STATEFULSET
spec:
  deployment:
    kind: StatefulSet
EOF

    # Wait for the Backstage pods to be running (not necessarily Ready - we just need them to exist for must-gather)
    log_info "Waiting for 2 Backstage pods for CR $BACKSTAGE_CR to be running..."
    TIMEOUT=300
    until [ "$(kubectl -n "$NS_OPERATOR" get pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | wc -w)" -ge 2 ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            log_error "Timed out waiting for 2 Backstage pods for CR $BACKSTAGE_CR to appear."
            dump_debug_info
            exit 1
        fi
    done
    OPERATOR_PODS=$(kubectl -n "$NS_OPERATOR" get pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR" -o jsonpath='{.items[*].metadata.name}')
    log_info "Found Backstage pods: $OPERATOR_PODS, waiting for them to be running..."
    if ! kubectl -n "$NS_OPERATOR" wait --for=jsonpath='{.status.phase}'=Running pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR" --timeout=3m; then
        log_error "Backstage pods for CR $BACKSTAGE_CR did not reach Running state."
        dump_debug_info
        exit 1
    fi
    log_info "Backstage pods for CR $BACKSTAGE_CR are now running."

    log_info "Waiting for Backstage pods for CR $BACKSTAGE_CR_STATEFULSET to be running..."
    STATEFULSET_POD=""
    TIMEOUT=300
    until STATEFULSET_POD=$(kubectl -n "$NS_STATEFULSET" get pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR_STATEFULSET" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$STATEFULSET_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            log_error "Timed out waiting for Backstage pod for CR $BACKSTAGE_CR_STATEFULSET to appear."
            dump_debug_info
            exit 1
        fi
    done
    log_info "Found Backstage pod: $STATEFULSET_POD, waiting for it to be running..."
    if ! kubectl -n "$NS_STATEFULSET" wait --for=jsonpath='{.status.phase}'=Running pod/"$STATEFULSET_POD" --timeout=3m; then
        log_error "Backstage pod $STATEFULSET_POD did not reach Running state."
        dump_debug_info
        exit 1
    fi
    log_info "Backstage pod $STATEFULSET_POD is now running."
else
    log_info "Skipping Operator setup"
fi

# ============================================================================
# RUN MUST-GATHER
# ============================================================================
log_info ""
log_info "=========================================="
log_info "Running must-gather"
log_info "=========================================="

if [ "$LOCAL_MODE" = true ]; then
    log_info "Running in local mode"
    if [ -n "$OVERLAY" ]; then
        log_warn "--overlay option is not applicable in local mode, ignoring"
    fi
    log_info "Running make clean-out run-local..."
    make clean-out run-local
    OUTPUT_DIR="./out"
    if [ ! -d "$OUTPUT_DIR" ]; then
        log_error "No output directory found at $OUTPUT_DIR!"
        exit 1
    fi
    log_info "Using output directory: $OUTPUT_DIR"
elif is_openshift; then
    log_info "Detected OpenShift cluster"
    if ! command -v oc &>/dev/null; then
        log_error "OpenShift cluster detected but 'oc' command not found. Please install the OpenShift CLI."
        exit 1
    fi
    if [ -n "$OVERLAY" ]; then
        log_warn "--overlay option is only applicable on Kubernetes, ignoring on OpenShift"
    fi
    log_info "Running make deploy-openshift..."
    make deploy-openshift \
        REGISTRY="$REGISTRY" \
        IMAGE_NAME="$IMAGE_NAME" \
        IMAGE_TAG="$IMAGE_TAG"
    # Find the output directory (most recent must-gather.local.* directory)
    OUTPUT_DIR=$(find . -maxdepth 1 -type d -name 'must-gather.local.*' -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2-)
    if [ -z "$OUTPUT_DIR" ]; then
        log_error "No output directory found!"
        exit 1
    fi
    log_info "Found output directory: $OUTPUT_DIR"
    # Find the actual data subdirectory (named after the image digest)
    OUTPUT_DIR=$(find "$OUTPUT_DIR" -mindepth 1 -maxdepth 1 -type d ! -name '.*' | head -1)
    if [ -z "$OUTPUT_DIR" ]; then
        log_error "No data subdirectory found in must-gather output!"
        exit 1
    fi
    log_info "Using data directory: $OUTPUT_DIR"
else
    log_info "Detected Kubernetes cluster (non-OpenShift)"
    log_info "Running make deploy-k8s..."
    make deploy-k8s \
        REGISTRY="$REGISTRY" \
        IMAGE_NAME="$IMAGE_NAME" \
        IMAGE_TAG="$IMAGE_TAG" \
        OVERLAY="$OVERLAY"
    # Find the output tarball (most recent one)
    OUTPUT_TARBALL=$(find . -maxdepth 1 -name 'rhdh-must-gather-output.k8s.*.tar.gz' -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2-)
    if [ -z "$OUTPUT_TARBALL" ]; then
        log_error "No output tarball found!"
        exit 1
    fi
    log_info "Found output tarball: $OUTPUT_TARBALL"
    # Extract and validate the output
    OUTPUT_DIR="${OUTPUT_TARBALL%.tar.gz}"
    log_info "Extracting to: $OUTPUT_DIR"
    mkdir -p "$OUTPUT_DIR"
    tar xzf "$OUTPUT_TARBALL" -C "$OUTPUT_DIR"
fi

# ============================================================================
# VALIDATION PHASE: Run validation scripts
# ============================================================================
log_info ""
log_info "=========================================="
log_info "Running validation checks"
log_info "=========================================="

VALIDATION_FAILURES=0

# Common validations (always run if any test is enabled)
if [ ${#ALL_NAMESPACES[@]} -gt 0 ]; then
    log_info ""
    log_info "Running common validations..."
    COMMON_ARGS=("$OUTPUT_DIR")
    if [ "$LOCAL_MODE" = true ]; then
        COMMON_ARGS+=(--local)
    fi
    # Join namespaces with comma
    NS_LIST=$(IFS=,; echo "${ALL_NAMESPACES[*]}")
    COMMON_ARGS+=(--namespaces "$NS_LIST")
    if ! "$SCRIPT_DIR/validate-common.sh" "${COMMON_ARGS[@]}"; then
        log_error "Common validation failed!"
        ((VALIDATION_FAILURES++))
    fi
fi

# Helm validation
if [ "$SKIP_HELM" = false ] && [ -n "$NS_HELM" ]; then
    log_info ""
    log_info "Running Helm validation..."
    if ! "$SCRIPT_DIR/validate-helm.sh" --validate --output-dir "$OUTPUT_DIR" --namespace "$NS_HELM" --release "$HELM_RELEASE"; then
        log_error "Helm validation failed!"
        ((VALIDATION_FAILURES++))
    fi
fi

# Standalone Helm validation
if [ "$SKIP_HELM_STANDALONE" = false ] && [ -n "$NS_STANDALONE" ]; then
    log_info ""
    log_info "Running standalone Helm validation..."
    STANDALONE_VALIDATE_ARGS=(--validate --output-dir "$OUTPUT_DIR" --namespace "$NS_STANDALONE" --deployment "$STANDALONE_DEPLOY")
    if [ -n "$STANDALONE_POSTGRES" ]; then
        STANDALONE_VALIDATE_ARGS+=(--postgres "$STANDALONE_POSTGRES")
    fi
    if ! "$SCRIPT_DIR/validate-helm-standalone.sh" "${STANDALONE_VALIDATE_ARGS[@]}"; then
        log_error "Standalone Helm validation failed!"
        ((VALIDATION_FAILURES++))
    fi
fi

# Operator validation
if [ "$SKIP_OPERATOR" = false ] && [ -n "$NS_OPERATOR" ]; then
    log_info ""
    log_info "Running Operator validation..."
    if ! "$SCRIPT_DIR/validate-operator.sh" --validate --output-dir "$OUTPUT_DIR" \
        --cr "$NS_OPERATOR:$BACKSTAGE_CR:2" \
        --cr "$NS_STATEFULSET:$BACKSTAGE_CR_STATEFULSET:1"; then
        log_error "Operator validation failed!"
        ((VALIDATION_FAILURES++))
    fi
fi

log_info ""
log_info "=========================================="
log_info "E2E Test Summary"
log_info "=========================================="

if [ $VALIDATION_FAILURES -eq 0 ]; then
    log_info "All validation checks passed!"
    exit 0
else
    log_error "$VALIDATION_FAILURES validation suite(s) failed!"
    exit 1
fi
