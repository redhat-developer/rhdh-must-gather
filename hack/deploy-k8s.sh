#!/usr/bin/env bash
#
# Run RHDH must-gather on a standard Kubernetes cluster using the Helm chart.
#
# Usage:
#   ./hack/deploy-k8s.sh [OPTIONS]
#
# Options:
#   --image <image>       Full image name (default: quay.io/rhdh-community/rhdh-must-gather:latest)
#   --namespace <ns>      Namespace to deploy must-gather in (default: rhdh-must-gather-<timestamp>)
#   --opts <options>      Additional options to pass to the gather script (quote multiple options)
#   --helm-set <sets>     Additional Helm --set flags (space-separated, e.g., "key1=val1 key2=val2")
#   --output <file>       Output file path (default: rhdh-must-gather-output.k8s.<timestamp>.tar.gz)
#   --timeout <duration>  Timeout for Helm install/upgrade (default: 60m)
#   --help                Show this help message
#
# Examples:
#   ./hack/deploy-k8s.sh
#   ./hack/deploy-k8s.sh --image quay.io/myorg/rhdh-must-gather:v1.0.0
#   ./hack/deploy-k8s.sh --namespace my-must-gather-ns
#   ./hack/deploy-k8s.sh --opts "--namespaces my-ns"
#   ./hack/deploy-k8s.sh --opts "--with-heap-dumps --namespaces my-ns"
#   ./hack/deploy-k8s.sh --opts "--with-heap-dumps --heap-dump-instances my-rhdh,dev-hub"
#   ./hack/deploy-k8s.sh --output ./debug-mustgather.tar.gz
#   ./hack/deploy-k8s.sh --image myimage:tag --opts "--with-secrets --namespaces my-ns"
#

set -euo pipefail

# Default values
DEFAULT_IMAGE="quay.io/rhdh-community/rhdh-must-gather:latest"
IMAGE="${DEFAULT_IMAGE}"
NAMESPACE=""
OPTS_STRING=""
HELM_SET_STRING=""
OUTPUT_FILE=""
HELM_TIMEOUT="60m"

# Parse named arguments
show_help() {
    sed -n '2,/^$/p' "$0" | sed 's/^#//; s/^ //; /^$/d'
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --image)
            IMAGE="$2"
            shift 2
            ;;
        --namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --opts)
            OPTS_STRING="$2"
            shift 2
            ;;
        --helm-set)
            HELM_SET_STRING="$2"
            shift 2
            ;;
        --output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --timeout)
            HELM_TIMEOUT="$2"
            shift 2
            ;;
        --help|-h)
            show_help
            ;;
        *)
            echo "Error: Unknown option: $1"
            echo "Use --help for usage information."
            exit 1
            ;;
    esac
done

# Extract image components
# Robust parsing that handles registry ports correctly:
#   quay.io/ns/img:1.2.3      -> registry=quay.io, repo=ns/img, tag=1.2.3
#   quay.io/ns/img            -> registry=quay.io, repo=ns/img, tag=latest
#   localhost:5000/ns/img:dev -> registry=localhost:5000, repo=ns/img, tag=dev
#   localhost:5000/ns/img     -> registry=localhost:5000, repo=ns/img, tag=latest

# Extract tag from the last path segment (after last /)
LAST_SEGMENT="${IMAGE##*/}"
if [[ "${LAST_SEGMENT}" == *":"* ]]; then
    IMAGE_TAG="${LAST_SEGMENT##*:}"
    # Remove the tag suffix from the image (quote IMAGE_TAG to prevent pattern matching)
    IMAGE_WITHOUT_TAG="${IMAGE%":${IMAGE_TAG}"}"
else
    IMAGE_TAG="latest"
    IMAGE_WITHOUT_TAG="${IMAGE}"
fi

# Extract registry (first component) and repository (everything after first /)
if [[ "${IMAGE_WITHOUT_TAG}" == *"/"* ]]; then
    IMAGE_REGISTRY="${IMAGE_WITHOUT_TAG%%/*}"
    IMAGE_REPO="${IMAGE_WITHOUT_TAG#*/}"
else
    # No slash means just an image name (e.g., "nginx")
    IMAGE_REGISTRY=""
    IMAGE_REPO="${IMAGE_WITHOUT_TAG}"
fi

# Generate namespace (if not provided) and output file
TIMESTAMP=$(date +%s)
if [[ -z "${NAMESPACE}" ]]; then
    NAMESPACE="rhdh-must-gather-${TIMESTAMP}"
fi
RELEASE_NAME="rhdh-must-gather"
if [[ -z "${OUTPUT_FILE}" ]]; then
    OUTPUT_FILE="rhdh-must-gather-output.k8s.${TIMESTAMP}.tar.gz"
elif [[ "${OUTPUT_FILE}" != *.tar.gz ]]; then
    OUTPUT_FILE="${OUTPUT_FILE}.tar.gz"
fi

echo "Testing against a regular K8s cluster using Helm chart..."
echo ""

# Check for required tools
if ! command -v kubectl &>/dev/null; then
    echo "Error: kubectl command not found."
    exit 1
fi

if ! command -v helm &>/dev/null; then
    echo "Error: helm command not found."
    exit 1
fi

# Check if namespace already exists, create if needed
CREATED_NAMESPACE=false
if kubectl get namespace "${NAMESPACE}" &>/dev/null; then
    echo "Using existing namespace: ${NAMESPACE}"
else
    echo "Creating namespace: ${NAMESPACE}"
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
    CREATED_NAMESPACE=true
fi

echo ""
echo "Installing/upgrading must-gather Helm release: ${RELEASE_NAME}"
echo "Namespace: ${NAMESPACE}"
echo "Image: ${IMAGE}"

# Create temporary values file
TMP_VALUES=$(mktemp)
trap 'rm -f "${TMP_VALUES}"' EXIT

# Build values file
cat > "${TMP_VALUES}" <<EOF
image:
EOF

if [[ -n "${IMAGE_REGISTRY}" ]]; then
    cat >> "${TMP_VALUES}" <<EOF
  registry: ${IMAGE_REGISTRY}
EOF
fi

cat >> "${TMP_VALUES}" <<EOF
  repository: ${IMAGE_REPO}
  tag: ${IMAGE_TAG}
EOF

# Parse OPTS_STRING and convert to gather.* values
if [[ -n "${OPTS_STRING}" ]]; then
    echo "" >> "${TMP_VALUES}"
    echo "gather:" >> "${TMP_VALUES}"

    # Parse options - collect heapDump settings separately to write as a single block
    EXTRA_ARGS=()
    HEAP_DUMP_ENABLED=""
    HEAP_DUMP_INSTANCES=""
    HEAP_DUMP_METHOD=""
    read -ra OPTS_ARRAY <<< "${OPTS_STRING}"
    i=0
    while [[ $i -lt ${#OPTS_ARRAY[@]} ]]; do
        opt="${OPTS_ARRAY[$i]}"
        case "${opt}" in
            --with-heap-dumps)
                HEAP_DUMP_ENABLED="true"
                ;;
            --heap-dump-instances)
                i=$((i + 1))
                if [[ $i -lt ${#OPTS_ARRAY[@]} && -n "${OPTS_ARRAY[$i]}" && "${OPTS_ARRAY[$i]}" != --* ]]; then
                    HEAP_DUMP_INSTANCES="${OPTS_ARRAY[$i]}"
                else
                    echo "Error: --heap-dump-instances requires a comma-separated list of instance names"
                    exit 1
                fi
                ;;
            --heap-dump-instances=*)
                HEAP_DUMP_INSTANCES="${opt#*=}"
                ;;
            --heap-dump-method)
                i=$((i + 1))
                if [[ $i -lt ${#OPTS_ARRAY[@]} && -n "${OPTS_ARRAY[$i]}" && "${OPTS_ARRAY[$i]}" != --* ]]; then
                    case "${OPTS_ARRAY[$i]}" in
                        inspector|sigusr2)
                            HEAP_DUMP_METHOD="${OPTS_ARRAY[$i]}"
                            ;;
                        *)
                            echo "Error: --heap-dump-method must be 'inspector' or 'sigusr2'"
                            exit 1
                            ;;
                    esac
                else
                    echo "Error: --heap-dump-method requires a value (inspector or sigusr2)"
                    exit 1
                fi
                ;;
            --heap-dump-method=*)
                case "${opt#*=}" in
                    inspector|sigusr2)
                        HEAP_DUMP_METHOD="${opt#*=}"
                        ;;
                    *)
                        echo "Error: --heap-dump-method must be 'inspector' or 'sigusr2'"
                        exit 1
                        ;;
                esac
                ;;
            --with-secrets)
                echo "  withSecrets: true" >> "${TMP_VALUES}"
                ;;
            --without-operator)
                echo "  withOperator: false" >> "${TMP_VALUES}"
                ;;
            --without-helm)
                echo "  withHelm: false" >> "${TMP_VALUES}"
                ;;
            --without-route)
                echo "  withRoute: false" >> "${TMP_VALUES}"
                ;;
            --without-ingress)
                echo "  withIngress: false" >> "${TMP_VALUES}"
                ;;
            --namespaces)
                i=$((i + 1))
                if [[ $i -lt ${#OPTS_ARRAY[@]} && -n "${OPTS_ARRAY[$i]}" && "${OPTS_ARRAY[$i]}" != --* ]]; then
                    # Convert comma-separated to YAML array
                    NS_VALUE="${OPTS_ARRAY[$i]}"
                    echo "  namespaces:" >> "${TMP_VALUES}"
                    IFS=',' read -ra NS_ARRAY <<< "${NS_VALUE}"
                    for ns in "${NS_ARRAY[@]}"; do
                        echo "    - ${ns}" >> "${TMP_VALUES}"
                    done
                else
                    echo "Error: --namespaces requires a comma-separated list of namespaces"
                    exit 1
                fi
                ;;
            --namespaces=*)
                # Handle --namespaces=ns1,ns2 format
                NS_VALUE="${opt#*=}"
                echo "  namespaces:" >> "${TMP_VALUES}"
                IFS=',' read -ra NS_ARRAY <<< "${NS_VALUE}"
                for ns in "${NS_ARRAY[@]}"; do
                    echo "    - ${ns}" >> "${TMP_VALUES}"
                done
                ;;
            *)
                # Unknown options go to extraArgs
                EXTRA_ARGS+=("${opt}")
                ;;
        esac
        i=$((i + 1))
    done

    # Write heapDump block if any heap dump options were specified
    if [[ -n "${HEAP_DUMP_ENABLED}" || -n "${HEAP_DUMP_INSTANCES}" || -n "${HEAP_DUMP_METHOD}" ]]; then
        echo "  heapDump:" >> "${TMP_VALUES}"
        if [[ -n "${HEAP_DUMP_ENABLED}" ]]; then
            echo "    enabled: true" >> "${TMP_VALUES}"
        fi
        if [[ -n "${HEAP_DUMP_METHOD}" ]]; then
            echo "    method: ${HEAP_DUMP_METHOD}" >> "${TMP_VALUES}"
        fi
        if [[ -n "${HEAP_DUMP_INSTANCES}" ]]; then
            echo "    instances: \"${HEAP_DUMP_INSTANCES}\"" >> "${TMP_VALUES}"
        fi
    fi

    # Add extra environment variables for heap dump configuration
    if [[ -n "${HEAP_DUMP_SIGUSR2_STABLE_SECONDS:-}" ]]; then
        {
            echo "  extraEnvVars:"
            echo "    - name: HEAP_DUMP_SIGUSR2_STABLE_SECONDS"
            echo "      value: \"${HEAP_DUMP_SIGUSR2_STABLE_SECONDS}\""
        } >> "${TMP_VALUES}"
    fi

    # Add extraArgs if any
    if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
        echo "  extraArgs:" >> "${TMP_VALUES}"
        for arg in "${EXTRA_ARGS[@]}"; do
            echo "    - \"${arg}\"" >> "${TMP_VALUES}"
        done
    fi
fi

echo "Using values file:"
echo "---"
cat "${TMP_VALUES}"
echo "---"
echo ""

# Build additional --set arguments
HELM_SET_ARGS=()
if [[ -n "${HELM_SET_STRING}" ]]; then
    read -ra HELM_SET_ARRAY <<< "${HELM_SET_STRING}"
    for item in "${HELM_SET_ARRAY[@]}"; do
        HELM_SET_ARGS+=(--set "${item}")
    done
fi

# Install or upgrade the Helm chart
helm upgrade --install "${RELEASE_NAME}" redhat-developer-hub-must-gather \
    --repo https://redhat-developer.github.io/rhdh-chart \
    --namespace "${NAMESPACE}" \
    --values "${TMP_VALUES}" \
    "${HELM_SET_ARGS[@]}"

echo ""
echo "Helm release installed, waiting for pod to be created..."

# Wait for the pod to exist
POD_SELECTOR="app.kubernetes.io/instance=${RELEASE_NAME},app.kubernetes.io/component=gather"
TIMEOUT_SECONDS=$(echo "${HELM_TIMEOUT}" | sed 's/m/*60/;s/h/*3600/;s/s//' | bc)
WAIT_START=$(date +%s)
while ! kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o name 2>/dev/null | grep -q .; do
    ELAPSED=$(($(date +%s) - WAIT_START))
    if [[ ${ELAPSED} -ge ${TIMEOUT_SECONDS} ]]; then
        echo "Error: Timed out waiting for pod to be created"
        exit 1
    fi
    sleep 2
done

echo "Pod created, waiting for gather container to start..."

# Wait for the gather init container to be running
while true; do
    ELAPSED=$(($(date +%s) - WAIT_START))
    if [[ ${ELAPSED} -ge ${TIMEOUT_SECONDS} ]]; then
        echo "Error: Timed out waiting for gather container to start"
        exit 1
    fi

    # Check if the init container is running or has completed
    CONTAINER_STATE=$(kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o jsonpath='{.items[0].status.initContainerStatuses[?(@.name=="gather")].state}' 2>/dev/null)
    if [[ "${CONTAINER_STATE}" == *"running"* ]] || [[ "${CONTAINER_STATE}" == *"terminated"* ]]; then
        break
    fi
    sleep 2
done

echo "Streaming gather logs..."
echo ""

# Stream logs from the gather init container (will exit when container completes)
# Use timeout to prevent hanging indefinitely
if ! timeout "${HELM_TIMEOUT}" kubectl -n "${NAMESPACE}" logs -l "${POD_SELECTOR}" -c gather -f 2>&1; then
    echo ""
    echo "Error: Gather container did not complete within timeout or failed"
    echo ""
    echo "Resources left in namespace ${NAMESPACE} for debugging."
    echo "To clean up manually, run:"
    echo "  helm uninstall ${RELEASE_NAME} -n ${NAMESPACE}"
    if [[ "${CREATED_NAMESPACE}" == "true" ]]; then
        echo "  kubectl delete namespace ${NAMESPACE}"
    fi
    exit 1
fi

echo ""
echo "Gather logs finished, waiting for init container to terminate..."

# Wait for the gather init container to terminate (in case logs exited early)
while true; do
    ELAPSED=$(($(date +%s) - WAIT_START))
    if [[ ${ELAPSED} -ge ${TIMEOUT_SECONDS} ]]; then
        echo "Error: Timed out waiting for gather init container to terminate"
        exit 1
    fi

    CONTAINER_STATE=$(kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o jsonpath='{.items[0].status.initContainerStatuses[?(@.name=="gather")].state}' 2>/dev/null)
    if [[ "${CONTAINER_STATE}" == *"terminated"* ]]; then
        echo "Init container terminated"
        break
    fi
    echo "  Init container still running... (${ELAPSED}s elapsed)"
    sleep 5
done

echo "Waiting for data-holder container to be ready..."

# Wait for the pod to be ready with progress output
# Use a separate 5-minute timeout for data-holder readiness
DATA_HOLDER_TIMEOUT=300
DATA_HOLDER_WAIT_START=$(date +%s)
while true; do
    ELAPSED=$(($(date +%s) - DATA_HOLDER_WAIT_START))
    if [[ ${ELAPSED} -ge ${DATA_HOLDER_TIMEOUT} ]]; then
        echo "Error: Timed out waiting for data-holder container to be ready (${DATA_HOLDER_TIMEOUT}s)"
        echo "Pod status:"
        kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o wide
        kubectl -n "${NAMESPACE}" describe pod -l "${POD_SELECTOR}" | tail -30
        exit 1
    fi

    # Check if pod is ready
    READY=$(kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
    if [[ "${READY}" == "True" ]]; then
        echo "Pod is ready"
        break
    fi

    # Show container status
    CONTAINER_STATUS=$(kubectl -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="data-holder")].state}' 2>/dev/null)
    echo "  Data-holder container state: ${CONTAINER_STATUS} (${ELAPSED}s elapsed)"
    sleep 5
done
echo ""
echo "Gather completed successfully"
echo ""

# Pull data from the data-holder container
echo "Pulling must-gather data from data-holder container..."
POD_NAME=$(kubectl -n "${NAMESPACE}" get pods -l "app.kubernetes.io/instance=${RELEASE_NAME},app.kubernetes.io/component=gather" -o jsonpath='{.items[0].metadata.name}')
echo "Pod: ${POD_NAME}"
if ! timeout 5m kubectl -n "${NAMESPACE}" exec "${POD_NAME}" -c data-holder -- tar czf - -C /must-gather . > "${OUTPUT_FILE}"; then
    echo "Error: Failed to pull data from data-holder container (timeout or error)"
    echo ""
    echo "Resources left in namespace ${NAMESPACE} for debugging."
    echo "To clean up manually, run:"
    echo "  helm uninstall ${RELEASE_NAME} -n ${NAMESPACE}"
    if [[ "${CREATED_NAMESPACE}" == "true" ]]; then
        echo "  kubectl delete namespace ${NAMESPACE}"
    fi
    exit 1
fi
echo "Data pulled successfully ($(du -h "${OUTPUT_FILE}" | cut -f1))"
echo ""

# Cleanup
if [[ "${CREATED_NAMESPACE}" == "true" ]]; then
    echo "Cleaning up Helm release and namespace..."
else
    echo "Cleaning up Helm release (keeping existing namespace ${NAMESPACE})..."
fi
helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}" --wait 2>/dev/null || true
if [[ "${CREATED_NAMESPACE}" == "true" ]]; then
    kubectl delete namespace "${NAMESPACE}" --wait=false 2>/dev/null || true
fi
echo ""

echo "Must-gather data saved to: ${OUTPUT_FILE}"
echo ""
echo "To extract the data, run:"
echo "  tar xzf ${OUTPUT_FILE}"
