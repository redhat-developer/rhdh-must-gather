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
#   --target-branch <branch> Target branch (used for defaults, default: main)
#   --operator-branch <branch> Override RHDH operator branch (default: derived from --target-branch)
#   --helm-chart-version <version> Override Helm chart version (default: auto-detected from --target-branch)
#   --helm-values-file <file> Override Helm values file (default: auto-generated from --target-branch)
#   --skip-helm         Skip Helm release test
#   --skip-helm-standalone Skip standalone Helm deployment test
#   --skip-operator     Skip Operator test
#   --with-heap-dumps   Collect heap dumps from ALL instances (nightly mode).
#                       Without this flag, heap dumps are only collected from RHDHSUPP-308 test instance.
#   --heap-dump-method <method>  Heap dump method: 'inspector' (default) or 'sigusr2'. Only used with --with-heap-dumps.
#   --helm-timeout <duration>  Timeout for Helm install/upgrade (default: 60m)
#   --help              Show this help message
#
# Examples:
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123 --skip-operator
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123 --skip-helm --skip-helm-standalone
#   ./tests/e2e/run-e2e-tests.sh --local
#

set -euo pipefail

# Get script directory for sourcing lib and calling test scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=tests/e2e/lib/test-utils.sh
source "$SCRIPT_DIR/lib/test-utils.sh"

show_help() {
    sed -n '2,/^$/p' "$0" | sed 's/^#//; s/^ //; /^$/d'
    exit 0
}

# Dump debug information to help troubleshoot CI failures
# This function is automatically triggered on non-zero exit via the cleanup trap
# shellcheck disable=SC2329
dump_debug_info() {
    log_warn "=== DEBUG INFO START ==="
    log_warn "Script exiting with error, collecting must-gather for debugging..."

    # Run must-gather to collect cluster state for debugging
    local debug_image="quay.io/rhdh-community/rhdh-must-gather:latest"
    make deploy-k8s \
        FULL_IMAGE_NAME="$debug_image" \
        OUTPUT_FILE=./e2e-cluster.mustgather \
        || log_warn "Failed to collect must-gather for debugging"

    log_warn "=== DEBUG INFO END : ./e2e-cluster.mustgather.tar.gz ==="
}

# Cleanup function to handle multiple cleanup tasks
CLEANUP_TASKS=()
# shellcheck disable=SC2329
cleanup() {
    local exit_code=$?
    # Run dump_debug_info if exiting with error (before cleanup destroys the test resources)
    if [ "$exit_code" -ne 0 ]; then
        dump_debug_info
    fi
    for cmd in "${CLEANUP_TASKS[@]}"; do
        log_info "Cleanup: $cmd"
        eval "$cmd" || true
    done
}
trap cleanup EXIT

# Default values
FULL_IMAGE_NAME=""
LOCAL_MODE=false
TARGET_BRANCH="main"
OPERATOR_BRANCH=""
HELM_CHART_VERSION=""
HELM_VALUES_FILE=""
SKIP_HELM=false
SKIP_HELM_STANDALONE=false
SKIP_OPERATOR=false
WITH_HEAP_DUMPS=false
HEAP_DUMP_METHOD=""
HELM_TIMEOUT=""

# Timeout for waiting for RHDH instances to be ready (in seconds)
RHDH_READY_TIMEOUT=600

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
        --with-heap-dumps)
            WITH_HEAP_DUMPS=true
            shift
            ;;
        --heap-dump-method)
            HEAP_DUMP_METHOD="$2"
            shift 2
            ;;
        --helm-timeout)
            HELM_TIMEOUT="$2"
            shift 2
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
if [ "$WITH_HEAP_DUMPS" = true ]; then
    log_info "Heap dump collection: ALL instances (--with-heap-dumps)"
    if [ -n "$HEAP_DUMP_METHOD" ]; then
        log_info "Heap dump method: $HEAP_DUMP_METHOD"
    fi
else
    log_info "Heap dump collection: RHDHSUPP-308 instance only"
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

    log_info "Deploying Helm release with 2 replicas..."
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
            main|release-1.9|release-1.[1-9][0-9])
                cat > "$TEMP_VALUES_FILE" <<EOF
route:
  enabled: false
upstream:
  backstage:
    replicas: 2
  postgresql:
    # Purposely disable the local database to simulate a misconfigured application (missing external database info)
    enabled: false
global:
  dynamic:
    # Faster startup by disabling all default dynamic plugins
    includes: []
EOF
                # Add NODE_OPTIONS for SIGUSR2 heap dump method
                # NOTE: Helm does not merge arrays, so we must include the default extraEnvVars
                # from the chart (BACKEND_SECRET, POSTGRESQL_ADMIN_PASSWORD) alongside NODE_OPTIONS,
                # otherwise the defaults would be lost.
                if [ "$HEAP_DUMP_METHOD" = "sigusr2" ]; then
                    cat >> "$TEMP_VALUES_FILE" <<'EOF'
  backstage:
    extraEnvVars:
      - name: BACKEND_SECRET
        valueFrom:
          secretKeyRef:
            key: backend-secret
            name: '{{ include "rhdh.backend-secret-name" $ }}'
      - name: POSTGRESQL_ADMIN_PASSWORD
        valueFrom:
          secretKeyRef:
            key: postgres-password
            name: '{{- include "rhdh.postgresql.secretName" . }}'
      - name: NODE_OPTIONS
        value: "--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"
EOF
                fi
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

    # Wait for the Helm-deployed RHDH pods to enter CreateContainerConfigError state (this is expected)
    log_info "Waiting for 2 Helm-deployed RHDH pods to enter CreateContainerConfigError state (this is expected)..."
    TIMEOUT=$RHDH_READY_TIMEOUT
    until [ "$(kubectl -n "$NS_HELM" get pods -l "app.kubernetes.io/instance=$HELM_RELEASE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | wc -w)" -ge 2 ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            log_error "Could not find 2 Helm-deployed RHDH pods in namespace $NS_HELM."
            exit 1
        fi
    done
    HELM_PODS=$(kubectl -n "$NS_HELM" get pods -l "app.kubernetes.io/instance=$HELM_RELEASE" -o jsonpath='{.items[*].metadata.name}')
    log_info "Found Helm pods: $HELM_PODS"
    if ! kubectl wait --for=jsonpath='{.status.containerStatuses[0].state.waiting.reason}=CreateContainerConfigError' pods -l "app.kubernetes.io/instance=$HELM_RELEASE" -n "$NS_HELM" --timeout=${RHDH_READY_TIMEOUT}s 2>/dev/null; then
        log_error "Helm-deployed pods did not reach CreateContainerConfigError state within expected time."
        exit 1
    fi
    log_info "Helm release '$HELM_RELEASE' with 2 replicas deployed successfully in namespace $NS_HELM"
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
    # Add NODE_OPTIONS for SIGUSR2 heap dump method
    # NOTE: Helm does not merge arrays, so we must include the default extraEnvVars
    # from the chart (BACKEND_SECRET, POSTGRESQL_ADMIN_PASSWORD) alongside NODE_OPTIONS,
    # otherwise the defaults would be lost.
    if [ "$HEAP_DUMP_METHOD" = "sigusr2" ]; then
        cat >> "$STANDALONE_VALUES_FILE" <<'EOF'
upstream:
  backstage:
    extraEnvVars:
      - name: BACKEND_SECRET
        valueFrom:
          secretKeyRef:
            key: backend-secret
            name: '{{ include "rhdh.backend-secret-name" $ }}'
      - name: POSTGRESQL_ADMIN_PASSWORD
        valueFrom:
          secretKeyRef:
            key: postgres-password
            name: '{{- include "rhdh.postgresql.secretName" . }}'
      - name: NODE_OPTIONS
        value: "--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"
EOF
    fi

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
    TIMEOUT=$RHDH_READY_TIMEOUT
    until STANDALONE_POD=$(kubectl -n "$NS_STANDALONE" get pods -l "app.kubernetes.io/instance=$STANDALONE_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$STANDALONE_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            break
        fi
    done
    if [ -z "$STANDALONE_POD" ]; then
        log_error "Could not find standalone-deployed RHDH pod in namespace $NS_STANDALONE."
        exit 1
    fi
    log_info "Found standalone-deployed pod: $STANDALONE_POD"
    if ! kubectl -n "$NS_STANDALONE" wait --for=jsonpath='{.status.phase}'=Running pod/"$STANDALONE_POD" --timeout=${RHDH_READY_TIMEOUT}s; then
        log_error "Standalone-deployed pod $STANDALONE_POD did not reach Running state."
        exit 1
    fi
    log_info "Standalone-deployed pod $STANDALONE_POD is running."

    # Get the deployment name
    STANDALONE_DEPLOY=$(kubectl -n "$NS_STANDALONE" get deployment -l "app.kubernetes.io/instance=$STANDALONE_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [ -z "$STANDALONE_DEPLOY" ]; then
        log_error "Could not find standalone deployment in namespace $NS_STANDALONE."
        exit 1
    fi

    # Get the PostgreSQL StatefulSet name (dependent service from subchart)
    STANDALONE_POSTGRES=$(kubectl -n "$NS_STANDALONE" get statefulset -l "app.kubernetes.io/managed-by=Helm,app.kubernetes.io/instance=$STANDALONE_RELEASE,app.kubernetes.io/name=postgresql" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [ -z "$STANDALONE_POSTGRES" ]; then
        log_warn "Could not find PostgreSQL StatefulSet in namespace $NS_STANDALONE (may not be part of this chart version)"
    else
        log_info "Found PostgreSQL StatefulSet: $STANDALONE_POSTGRES"
        kubectl -n "$NS_STANDALONE" wait --for=jsonpath='{.status.phase}'=Running pod/"${STANDALONE_POSTGRES}-0" --timeout=${RHDH_READY_TIMEOUT}s 2>/dev/null || true
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
    if ! kubectl -n rhdh-operator wait --for=condition=Available deployment/rhdh-operator --timeout=${RHDH_READY_TIMEOUT}s; then
        log_error "Timed out waiting for rhdh-operator deployment to be available."
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

    # Create ConfigMap to disable default dynamic plugins for faster startup
    DYNAMIC_PLUGINS_CM="dynamic-plugins-config"
    log_info "Creating dynamic plugins ConfigMap to disable defaults..."
    for ns in "$NS_OPERATOR" "$NS_STATEFULSET"; do
        kubectl -n "$ns" apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: $DYNAMIC_PLUGINS_CM
data:
  dynamic-plugins.yaml: |
    includes: []
EOF
    done

    log_info "Deploying Backstage CR (kind: Deployment in v1alpha4)..."
    BACKSTAGE_CR="my-op"
    # Build CR spec - add NODE_OPTIONS for SIGUSR2 heap dump method
    BACKSTAGE_CR_EXTRA_ENVS=""
    if [ "$HEAP_DUMP_METHOD" = "sigusr2" ]; then
        BACKSTAGE_CR_EXTRA_ENVS='
    extraEnvs:
      envs:
        - name: NODE_OPTIONS
          value: "--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"'
    fi
    kubectl -n "$NS_OPERATOR" apply -f - <<EOF
apiVersion: rhdh.redhat.com/v1alpha4
kind: Backstage
metadata:
  name: $BACKSTAGE_CR
spec:
  application:
    dynamicPluginsConfigMapName: $DYNAMIC_PLUGINS_CM
$BACKSTAGE_CR_EXTRA_ENVS
EOF

    log_info "Deploying Backstage CR (kind: StatefulSet in v1alpha5)..."
    BACKSTAGE_CR_STATEFULSET="my-op-statefulset"
    # Build CR spec - add NODE_OPTIONS for SIGUSR2 heap dump method
    BACKSTAGE_CR_STS_EXTRA=""
    if [ "$HEAP_DUMP_METHOD" = "sigusr2" ]; then
        BACKSTAGE_CR_STS_EXTRA='
    extraEnvs:
      envs:
        - name: NODE_OPTIONS
          value: "--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"'
    fi
    kubectl -n "$NS_STATEFULSET" apply -f - <<EOF
apiVersion: rhdh.redhat.com/v1alpha5
kind: Backstage
metadata:
  name: $BACKSTAGE_CR_STATEFULSET
spec:
  deployment:
    kind: StatefulSet
  application:
    dynamicPluginsConfigMapName: $DYNAMIC_PLUGINS_CM
$BACKSTAGE_CR_STS_EXTRA
EOF

    # Wait for the Backstage pod to be running (not necessarily Ready - we just need it to exist for must-gather)
    log_info "Waiting for Backstage pod for CR $BACKSTAGE_CR to be running..."
    OPERATOR_POD=""
    TIMEOUT=$RHDH_READY_TIMEOUT
    until OPERATOR_POD=$(kubectl -n "$NS_OPERATOR" get pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$OPERATOR_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            log_error "Timed out waiting for Backstage pod for CR $BACKSTAGE_CR to appear."
            exit 1
        fi
    done
    log_info "Found Backstage pod: $OPERATOR_POD, waiting for it to be running..."
    if ! kubectl -n "$NS_OPERATOR" wait --for=jsonpath='{.status.phase}'=Running pod/"$OPERATOR_POD" --timeout=${RHDH_READY_TIMEOUT}s; then
        log_error "Backstage pod $OPERATOR_POD did not reach Running state."
        exit 1
    fi
    log_info "Backstage pod $OPERATOR_POD is now running."

    log_info "Waiting for Backstage pods for CR $BACKSTAGE_CR_STATEFULSET to be running..."
    STATEFULSET_POD=""
    TIMEOUT=$RHDH_READY_TIMEOUT
    until STATEFULSET_POD=$(kubectl -n "$NS_STATEFULSET" get pods -l "rhdh.redhat.com/app=backstage-$BACKSTAGE_CR_STATEFULSET" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$STATEFULSET_POD" ]; do
        sleep 2
        TIMEOUT=$((TIMEOUT - 2))
        if [ $TIMEOUT -le 0 ]; then
            log_error "Timed out waiting for Backstage pod for CR $BACKSTAGE_CR_STATEFULSET to appear."
            exit 1
        fi
    done
    log_info "Found Backstage pod: $STATEFULSET_POD, waiting for it to be running..."
    if ! kubectl -n "$NS_STATEFULSET" wait --for=jsonpath='{.status.phase}'=Running pod/"$STATEFULSET_POD" --timeout=${RHDH_READY_TIMEOUT}s; then
        log_error "Backstage pod $STATEFULSET_POD did not reach Running state."
        exit 1
    fi
    log_info "Backstage pod $STATEFULSET_POD is now running."
else
    log_info "Skipping Operator setup"
fi

# --- RHDHSUPP-308 Test Setup (standalone manifest from helm template) ---
NS_RHDHSUPP308="test-e2e-rhdhsupp308-$TIMESTAMP"
RHDHSUPP308_INSTANCE="rhdhsupp-308"
RHDHSUPP308_DEPLOY="rhdhsupp-308-backstage"
log_info "Creating namespace: $NS_RHDHSUPP308"
kubectl create namespace "$NS_RHDHSUPP308"
CLEANUP_TASKS+=("kubectl delete namespace $NS_RHDHSUPP308 --wait=false")
ALL_NAMESPACES+=("$NS_RHDHSUPP308")

log_info "Deploying RHDHSUPP-308 test manifest..."
RHDHSUPP308_MANIFEST="$SCRIPT_DIR/testdata/RHDHSUPP-308/helm_template_output.test.yaml"
if [ ! -f "$RHDHSUPP308_MANIFEST" ]; then
    log_error "RHDHSUPP-308 test manifest not found: $RHDHSUPP308_MANIFEST"
    exit 1
fi
kubectl apply -n "$NS_RHDHSUPP308" -f "$RHDHSUPP308_MANIFEST"

# Wait for PostgreSQL to be ready first
log_info "Waiting for PostgreSQL pod to be ready..."
kubectl wait --for=condition=Ready pod -l "app.kubernetes.io/name=postgresql,app.kubernetes.io/instance=$RHDHSUPP308_INSTANCE" \
    -n "$NS_RHDHSUPP308" --timeout=${RHDH_READY_TIMEOUT}s 2>/dev/null || log_warn "PostgreSQL pod not ready, continuing..."

# Wait for backstage pod to be running
log_info "Waiting for RHDHSUPP-308 backstage pod to be running..."
RHDHSUPP308_POD=""
TIMEOUT=$RHDH_READY_TIMEOUT
until RHDHSUPP308_POD=$(kubectl -n "$NS_RHDHSUPP308" get pods -l "app.kubernetes.io/name=backstage,app.kubernetes.io/instance=$RHDHSUPP308_INSTANCE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$RHDHSUPP308_POD" ]; do
    sleep 2
    TIMEOUT=$((TIMEOUT - 2))
    if [ $TIMEOUT -le 0 ]; then
        log_error "Timed out waiting for RHDHSUPP-308 backstage pod."
        kubectl get pods -n "$NS_RHDHSUPP308"
        exit 1
    fi
done
log_info "Found RHDHSUPP-308 pod: $RHDHSUPP308_POD"
if ! kubectl -n "$NS_RHDHSUPP308" wait --for=jsonpath='{.status.phase}'=Running pod/"$RHDHSUPP308_POD" --timeout=${RHDH_READY_TIMEOUT}s; then
    log_error "RHDHSUPP-308 pod $RHDHSUPP308_POD did not reach Running state."
    exit 1
fi
log_info "RHDHSUPP-308 pod $RHDHSUPP308_POD is running."

# Wait a bit for Node.js to fully start (needed for heap dump collection)
log_info "Waiting for Node.js process to start..."
sleep 10

# ============================================================================
# RUN MUST-GATHER
# ============================================================================
log_info ""
log_info "=========================================="
log_info "Running must-gather"
log_info "=========================================="

GATHER_OPTS="--with-heap-dumps"
GATHER_HELM_SET=""
if [ "$WITH_HEAP_DUMPS" = true ]; then
    # Nightly mode: collect from all instances, with optional method override
    if [ -n "$HEAP_DUMP_METHOD" ]; then
        GATHER_OPTS="$GATHER_OPTS --heap-dump-method $HEAP_DUMP_METHOD"
        # For SIGUSR2, reduce stability wait time for E2E tests (default is 150s)
        if [ "$HEAP_DUMP_METHOD" = "sigusr2" ]; then
            export HEAP_DUMP_SIGUSR2_STABLE_SECONDS=30
            GATHER_HELM_SET="gather.extraEnvVars[0].name=HEAP_DUMP_SIGUSR2_STABLE_SECONDS,gather.extraEnvVars[0].value=30"
        fi
    fi
else
    # Regular E2E: collect only from RHDHSUPP-308 instance to speed up testing
    GATHER_OPTS="$GATHER_OPTS --heap-dump-instances $RHDHSUPP308_INSTANCE"
fi

if [ "$LOCAL_MODE" = true ]; then
    log_info "Running in local mode"
    log_info "Running make clean-out run-local..."
    make clean-out run-local OPTS="$GATHER_OPTS"
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
    log_info "Running make deploy-openshift..."
    make deploy-openshift \
        REGISTRY="$REGISTRY" \
        IMAGE_NAME="$IMAGE_NAME" \
        IMAGE_TAG="$IMAGE_TAG" \
        OPTS="$GATHER_OPTS"
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
        OPTS="$GATHER_OPTS" \
        ${GATHER_HELM_SET:+HELM_SET="$GATHER_HELM_SET"} \
        ${HELM_TIMEOUT:+HELM_TIMEOUT="$HELM_TIMEOUT"}
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
    if ! "$SCRIPT_DIR/validate-helm.sh" --validate --output-dir "$OUTPUT_DIR" --namespace "$NS_HELM" --release "$HELM_RELEASE" --replicas 2; then
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
        --cr "$NS_OPERATOR:$BACKSTAGE_CR:1" \
        --cr "$NS_STATEFULSET:$BACKSTAGE_CR_STATEFULSET:1"; then
        log_error "Operator validation failed!"
        ((VALIDATION_FAILURES++))
    fi
fi

# RHDHSUPP-308 standalone validation (always runs)
log_info ""
log_info "Running RHDHSUPP-308 standalone validation..."
RHDHSUPP308_POSTGRES="rhdhsupp-308-postgresql"
if ! "$SCRIPT_DIR/validate-helm-standalone.sh" --validate \
    --output-dir "$OUTPUT_DIR" \
    --namespace "$NS_RHDHSUPP308" \
    --deployment "$RHDHSUPP308_DEPLOY" \
    --postgres "$RHDHSUPP308_POSTGRES"; then
    log_error "RHDHSUPP-308 standalone validation failed!"
    ((VALIDATION_FAILURES++))
fi

# Heap dump validation for RHDHSUPP-308 instance (always runs since heap dumps are always collected)
log_info ""
log_info "Running heap dump validation for RHDHSUPP-308 instance..."
if ! "$SCRIPT_DIR/validate-heap-dumps.sh" --validate \
    --output-dir "$OUTPUT_DIR" \
    --namespace "$NS_RHDHSUPP308" \
    --deployment "$RHDHSUPP308_DEPLOY" \
    --type standalone \
    --require-success; then
    log_error "Heap dump validation failed for RHDHSUPP-308!"
    ((VALIDATION_FAILURES++))
fi

# Additional heap dump validations (only in nightly mode with --with-heap-dumps)
if [ "$WITH_HEAP_DUMPS" = true ]; then
    # Standalone Helm heap dump validation
    if [ "$SKIP_HELM_STANDALONE" = false ] && [ -n "$NS_STANDALONE" ]; then
        log_info ""
        log_info "Running heap dump validation for standalone Helm deployment..."
        if ! "$SCRIPT_DIR/validate-heap-dumps.sh" --validate \
            --output-dir "$OUTPUT_DIR" \
            --namespace "$NS_STANDALONE" \
            --deployment "$STANDALONE_DEPLOY" \
            --type standalone \
            --require-success; then
            log_error "Heap dump validation failed for standalone Helm!"
            ((VALIDATION_FAILURES++))
        fi
    fi

    # Operator CR heap dump validation (Deployment)
    if [ "$SKIP_OPERATOR" = false ] && [ -n "$NS_OPERATOR" ]; then
        log_info ""
        log_info "Running heap dump validation for Operator CR (Deployment)..."
        if ! "$SCRIPT_DIR/validate-heap-dumps.sh" --validate \
            --output-dir "$OUTPUT_DIR" \
            --namespace "$NS_OPERATOR" \
            --deployment "backstage-$BACKSTAGE_CR" \
            --type operator \
            --cr "$BACKSTAGE_CR" \
            --require-success; then
            log_error "Heap dump validation failed for Operator CR $BACKSTAGE_CR!"
            ((VALIDATION_FAILURES++))
        fi
    fi

    # Operator CR heap dump validation (StatefulSet)
    if [ "$SKIP_OPERATOR" = false ] && [ -n "$NS_STATEFULSET" ]; then
        log_info ""
        log_info "Running heap dump validation for Operator CR (StatefulSet)..."
        if ! "$SCRIPT_DIR/validate-heap-dumps.sh" --validate \
            --output-dir "$OUTPUT_DIR" \
            --namespace "$NS_STATEFULSET" \
            --deployment "backstage-$BACKSTAGE_CR_STATEFULSET" \
            --type operator \
            --cr "$BACKSTAGE_CR_STATEFULSET" \
            --require-success; then
            log_error "Heap dump validation failed for Operator CR $BACKSTAGE_CR_STATEFULSET!"
            ((VALIDATION_FAILURES++))
        fi
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
