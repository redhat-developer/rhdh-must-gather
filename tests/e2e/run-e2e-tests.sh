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
#   --help              Show this help message
#
# Examples:
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123
#   ./tests/e2e/run-e2e-tests.sh --image quay.io/rhdh-community/rhdh-must-gather:pr-123 --overlay with-heap-dumps
#   ./tests/e2e/run-e2e-tests.sh --local
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

show_help() {
    sed -n '2,/^$/p' "$0" | sed 's/^#//; s/^ //; /^$/d'
    exit 0
}

# Detect if we're running on an OpenShift cluster
is_openshift() {
    # Check if the cluster has OpenShift-specific API resources
    kubectl api-resources --api-group=config.openshift.io 2>/dev/null | grep -q clusterversion
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

# Ensure we're in the project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

log_info "Working directory: $PROJECT_ROOT"

# Deploy some instances of RHDH
log_info "Deploying RHDH instances..."
TIMESTAMP=$(date +%s)
NS="test-e2e-$TIMESTAMP"
kubectl create namespace "$NS"
CLEANUP_TASKS+=("kubectl delete namespace $NS --wait=false")

# Helm
log_info "Deploying Helm release..."
TEMP_VALUES_FILE="$(mktemp)"
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

HELM_RELEASE="my-rhdh-helm"
## TODO: consider specifying a specific version of the Helm chart to test
helm -n "$NS" install "$HELM_RELEASE" backstage \
    --repo "https://redhat-developer.github.io/rhdh-chart" \
    --values "$TEMP_VALUES_FILE"
# Wait for the Helm-deployed RHDH pod to enter CreateContainerConfigError state (this is expected)
log_info "Waiting for Helm-deployed RHDH pod to enter CreateContainerConfigError state (this is expected)..."
HELM_POD=""
TIMEOUT=60
until HELM_POD=$(kubectl -n "$NS" get pods -l "app.kubernetes.io/instance=$HELM_RELEASE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) && [ -n "$HELM_POD" ]; do
    sleep 2
    TIMEOUT=$((TIMEOUT - 2))
    if [ $TIMEOUT -le 0 ]; then
        break
    fi
done
if [ -z "$HELM_POD" ]; then
    log_error "Could not find Helm-deployed RHDH pod in namespace $NS."
    exit 1
fi
if ! kubectl wait --for=jsonpath='{.status.containerStatuses[0].state.waiting.reason}=CreateContainerConfigError' pod/"$HELM_POD" -n "$NS" --timeout=5m 2>/dev/null; then
    POD_REASON=$(kubectl -n "$NS" get pod "$HELM_POD" -o jsonpath='{.status.containerStatuses[0].state.waiting.reason}' 2>/dev/null)
    log_error "Helm-deployed pod $HELM_POD did not reach CreateContainerConfigError state (current: $POD_REASON) within expected time. Test may not be operating under expected conditions."
    exit 1
fi

# Operator
log_info "Deploying RHDH Operator..."
OPERATOR_BRANCH="main"
OPERATOR_MANIFEST="https://raw.githubusercontent.com/redhat-developer/rhdh-operator/$OPERATOR_BRANCH/dist/rhdh/install.yaml"
kubectl apply -f "$OPERATOR_MANIFEST"
CLEANUP_TASKS+=("kubectl delete -f $OPERATOR_MANIFEST --wait=false")
log_info "Waiting for rhdh-operator deployment to be available in rhdh-operator namespace..."
if ! kubectl -n rhdh-operator wait --for=condition=Available deployment/rhdh-operator --timeout=5m; then
    log_error "Timed out waiting for rhdh-operator deployment to be available."
    exit 1
fi
log_info "rhdh-operator deployment is now available."

NS_STATEFULSET="test-e2e-$TIMESTAMP-2"
kubectl create namespace "$NS_STATEFULSET"
CLEANUP_TASKS+=("kubectl delete namespace $NS_STATEFULSET --wait=false")

log_info "Deploying Backstage CR (kind: Deployment in v1alpha4) with 2 replicas..."
BACKSTAGE_CR="my-rhdh-op"
kubectl -n "$NS" apply -f - <<EOF
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
# Added in 1.9
log_info "Deploying Backstage CR (kind: StatefulSet in v1alpha5)..."
BACKSTAGE_CR_STATEFULSET="my-rhdh-op-statefulset"
kubectl -n "$NS_STATEFULSET" apply -f - <<EOF
apiVersion: rhdh.redhat.com/v1alpha5
kind: Backstage
metadata:
  name: $BACKSTAGE_CR_STATEFULSET
spec:
  deployment:
    kind: StatefulSet
EOF

# wait until the Backstage CR is reconciled
log_info "Waiting for Backstage CR $BACKSTAGE_CR to be reconciled..."
if ! kubectl -n "$NS" wait --for='jsonpath={.status.conditions[?(@.type=="Deployed")].reason}=Deployed' backstage/$BACKSTAGE_CR --timeout=5m; then
    log_error "Timed out waiting for Backstage CR $BACKSTAGE_CR to be reconciled."
    exit 1
fi
log_info "Backstage CR $BACKSTAGE_CR is now ready and deployed."

# wait until the Backstage CR is reconciled
log_info "Waiting for Backstage CR $BACKSTAGE_CR_STATEFULSET to be reconciled..."
if ! kubectl -n "$NS_STATEFULSET" wait --for='jsonpath={.status.conditions[?(@.type=="Deployed")].reason}=Deployed' backstage/$BACKSTAGE_CR_STATEFULSET --timeout=5m; then
    log_error "Timed out waiting for Backstage CR $BACKSTAGE_CR_STATEFULSET to be reconciled."
    exit 1
fi
log_info "Backstage CR $BACKSTAGE_CR_STATEFULSET is now ready and deployed."

# Detect cluster type and run the appropriate deployment
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

# Validation checks
ERRORS=0

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

log_info ""
log_info "=========================================="
log_info "Validating must-gather output structure"
log_info "=========================================="

# Check required files
if [ "$LOCAL_MODE" = true ]; then
    log_info "○ Skipping must-gather.log check in local mode (logs go to console)"
else
    check_file_not_empty "$OUTPUT_DIR/must-gather.log" "must-gather container logs"
fi

check_file_not_empty "$OUTPUT_DIR/version" "version file"

check_file_not_empty "$OUTPUT_DIR/sanitization-report.txt" "sanitization report"

check_file_not_empty "$OUTPUT_DIR/platform/platform.txt" "platform information file (text)"
check_file_not_empty "$OUTPUT_DIR/platform/platform.json" "platform information file (JSON)"
check_file_valid_json "$OUTPUT_DIR/platform/platform.json" "platform information file (JSON)"
PLT=$(jq -r '.platform' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$PLT" ]; then
    log_error "✗ platform is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
fi
UNDERLYING_PLT=$(jq -r '.underlying' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$UNDERLYING_PLT" ]; then
    log_error "✗ 'underlying' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
fi
K8S_VER=$(jq -r '.k8sVersion' "$OUTPUT_DIR/platform/platform.json")
if [ -z "$K8S_VER" ]; then
    log_error "✗ 'k8sVersion' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
    ((ERRORS++))
fi
if is_openshift; then
    OCP_VER=$(jq -r '.ocpVersion' "$OUTPUT_DIR/platform/platform.json")
    if [ -z "$OCP_VER" ]; then
        log_error "✗ 'ocpVersion' is empty in platform information file (JSON): $OUTPUT_DIR/platform/platform.json"
        ((ERRORS++))
    fi
fi

check_dir_not_empty "$OUTPUT_DIR/namespace-inspect" "namespace-inspect directory"
check_dir_not_empty "$OUTPUT_DIR/namespace-inspect/namespaces/rhdh-operator" "rhdh-operator in namespace-inspect directory"
check_dir_not_empty "$OUTPUT_DIR/namespace-inspect/namespaces/$NS" "test namespace in namespace-inspect directory"

check_dir_not_empty "$OUTPUT_DIR/helm" "Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "release info text"
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "$HELM_RELEASE" "$HELM_RELEASE is listed in the Helm releases list"
check_file_contains "$OUTPUT_DIR/helm/all-rhdh-releases.txt" "$NS" "$NS is displayed in the Helm releases list"

check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS" "$NS namespace in Helm collection directory"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/_configmaps" "$NS namespace configmaps in Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/values.yaml" "values.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/all-values.yaml" "all-values.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/manifest.yaml" "manifest.yaml"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/hooks.yaml" "hooks.yaml"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/deployment" "all deployment data in Helm collection directory"
check_file_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/deployment/logs-app.txt" "values.yaml"
check_dir_not_empty "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/deployment/pods" "all pod data in Helm collection directory"
# Processes are only collected from running pods; the Helm deployment is intentionally misconfigured (CreateContainerConfigError)
# so the processes directory should NOT exist (or be empty if created)
if [ -d "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/deployment/processes" ]; then
    # If processes dir exists, it should be empty (no running pods to collect from)
    if [ -n "$(ls -A "$OUTPUT_DIR/helm/releases/ns=$NS/$HELM_RELEASE/deployment/processes" 2>/dev/null)" ]; then
        log_error "✗ Unexpectedly found process data in processes directory (expected empty - no running pods)"
        ((ERRORS++))
    else
        log_info "✓ Correctly found empty processes directory (no running pods)"
    fi
else
    log_info "✓ Correctly missing processes directory (expected - no running pods)"
fi

check_dir_not_empty "$OUTPUT_DIR/operator" "Operator collection directory"
check_dir_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator" "rhdh-operator namespace in Operator collection directory"
check_dir_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator/configs" "rhdh-operator configmaps in Operator collection directory"
check_dir_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator/deployments" "rhdh-operator deployments in Operator collection directory"
check_file_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator/logs.txt" "logs.txt"
check_dir_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator/configs" "rhdh-operator configmaps in Operator collection directory"
check_dir_not_empty "$OUTPUT_DIR/operator/ns=rhdh-operator/deployments" "rhdh-operator deployments in Operator collection directory"

check_dir_not_empty "$OUTPUT_DIR/operator/crds" "CRDs in Operator collection directory"
check_file_contains "$OUTPUT_DIR/operator/crds/all-crds.txt" "backstages.rhdh.redhat.com" "Backstage CRD is listed in the All CRDs list"
check_file_not_empty "$OUTPUT_DIR/operator/crds/backstages.rhdh.redhat.com.describe.txt" "Backstage CRD in Operator collection directory"
check_file_not_empty "$OUTPUT_DIR/operator/crds/backstages.rhdh.redhat.com.yaml" "Backstage CRD definition in Operator collection directory"

check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs" "Backstage CRs in Operator collection directory"
check_file_not_empty "$OUTPUT_DIR/operator/backstage-crs/all-backstage-crs.txt" "All Backstage CRs in Operator collection directory"
check_file_contains "$OUTPUT_DIR/operator/backstage-crs/all-backstage-crs.txt" "$BACKSTAGE_CR" "Backstage CR is listed in the All CRDs list"
check_file_contains "$OUTPUT_DIR/operator/backstage-crs/all-backstage-crs.txt" "$BACKSTAGE_CR_STATEFULSET" "Backstage CR (kind: StatefulSet) is listed in the All CRDs list"
cr=$BACKSTAGE_CR
expected_replicas=2
for ns in "$NS" "$NS_STATEFULSET"; do
    if [ "$ns" == "$NS_STATEFULSET" ]; then
        cr=$BACKSTAGE_CR_STATEFULSET
        expected_replicas=1
    fi
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns" "$ns namespace in Backstage CRs in Operator collection directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/_configmaps" "$ns namespace configmaps in Backstage CRs in Operator collection directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr" "$cr in Backstage CRs in Operator collection directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment" "all deployment data in $cr in Backstage CRs in Operator collection directory"
    check_file_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment/logs-app.txt" "Backstage CR Deployment logs in Operator collection directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment/pods" "all pod data in $cr in Backstage CRs in Operator collection directory"
    # Process list collection (from running pods) - now organized by pod
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment/processes" "processes directory in $cr deployment"
    # Verify process collection from each pod (organized in per-pod subdirectories)
    pod_dirs=$(find "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment/processes" -mindepth 1 -maxdepth 1 -type d -name 'pod=*' 2>/dev/null | wc -l)
    if [ "$pod_dirs" -eq "$expected_replicas" ]; then
        log_info "✓ Found $pod_dirs pod process directories (expected: $expected_replicas replicas)"
    else
        log_error "✗ Expected $expected_replicas pod process directories, found $pod_dirs"
        ((ERRORS++))
    fi
    # Validate process files in each pod directory
    for pod_dir in "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/deployment/processes"/pod=*; do
        if [ -d "$pod_dir" ]; then
            pod_name=$(basename "$pod_dir")
            check_file_not_empty "$pod_dir/container=backstage-backend.txt" "backstage-backend container process list in $pod_name"
            check_file_contains "$pod_dir/container=backstage-backend.txt" "PID" "process list header in $pod_name"
            check_file_contains "$pod_dir/container=backstage-backend.txt" "node" "Node.js process in process list in $pod_name"
        fi
    done
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/db-statefulset" "all deployment data in $cr in Backstage CRs in Operator collection directory"
    check_file_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/db-statefulset/logs-db.txt" "Backstage CR DB StatefulSet logs in Operator collection directory"
    check_dir_not_empty "$OUTPUT_DIR/operator/backstage-crs/ns=$ns/$cr/db-statefulset/pods" "all DB StatefulSet pods data in $cr in Backstage CRs in Operator collection directory"
done

## Optional (depending on the flags used)
if [ -d "$OUTPUT_DIR/cluster-info" ]; then
    log_info "✓ Found cluster info data directory"
    check_dir_not_empty "$OUTPUT_DIR/cluster-info" "cluster info data directory"
else
    log_warn "○ Cluster info not present (expected - collection is opt-in)"
fi

log_info ""
log_info "=========================================="
log_info "E2E Test Summary"
log_info "=========================================="

if [ $ERRORS -eq 0 ]; then
    log_info "All validation checks passed!"
    exit 0
else
    log_error "$ERRORS validation check(s) failed!"
    exit 1
fi
