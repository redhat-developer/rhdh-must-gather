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
                if [[ $i -lt ${#OPTS_ARRAY[@]} ]]; then
                    HEAP_DUMP_INSTANCES="${OPTS_ARRAY[$i]}"
                fi
                ;;
            --heap-dump-instances=*)
                HEAP_DUMP_INSTANCES="${opt#*=}"
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
                if [[ $i -lt ${#OPTS_ARRAY[@]} ]]; then
                    # Convert comma-separated to YAML array
                    NS_VALUE="${OPTS_ARRAY[$i]}"
                    echo "  namespaces:" >> "${TMP_VALUES}"
                    IFS=',' read -ra NS_ARRAY <<< "${NS_VALUE}"
                    for ns in "${NS_ARRAY[@]}"; do
                        echo "    - ${ns}" >> "${TMP_VALUES}"
                    done
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
    if [[ -n "${HEAP_DUMP_ENABLED}" || -n "${HEAP_DUMP_INSTANCES}" ]]; then
        echo "  heapDump:" >> "${TMP_VALUES}"
        if [[ -n "${HEAP_DUMP_ENABLED}" ]]; then
            echo "    enabled: true" >> "${TMP_VALUES}"
        fi
        if [[ -n "${HEAP_DUMP_INSTANCES}" ]]; then
            echo "    instances: \"${HEAP_DUMP_INSTANCES}\"" >> "${TMP_VALUES}"
        fi
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
helm upgrade --install "${RELEASE_NAME}" rhdh-must-gather \
    --repo https://redhat-developer.github.io/rhdh-chart \
    --namespace "${NAMESPACE}" \
    --values "${TMP_VALUES}" \
    "${HELM_SET_ARGS[@]}" \
    --wait \
    --timeout 60m

echo ""
echo "Helm release installed, waiting for gather to complete..."

# Wait for the pod to be ready (gather init container completed)
if ! kubectl -n "${NAMESPACE}" wait --for=condition=ready pod -l "app.kubernetes.io/instance=${RELEASE_NAME},app.kubernetes.io/component=gather" --timeout=3600s 2>&1; then
    echo "Error: Gather did not complete within timeout"
    echo ""
    echo "Gather logs:"
    kubectl -n "${NAMESPACE}" logs -l "app.kubernetes.io/instance=${RELEASE_NAME},app.kubernetes.io/component=gather" -c gather --tail=50 || true
    echo ""
    echo "Resources left in namespace ${NAMESPACE} for debugging."
    echo "To clean up manually, run:"
    echo "  helm uninstall ${RELEASE_NAME} -n ${NAMESPACE}"
    if [[ "${CREATED_NAMESPACE}" == "true" ]]; then
        echo "  kubectl delete namespace ${NAMESPACE}"
    fi
    exit 1
fi
echo "Gather completed successfully"
echo ""

# Pull data from the data-holder container
echo "Pulling must-gather data from data-holder container..."
kubectl -n "${NAMESPACE}" exec "deploy/${RELEASE_NAME}" -c data-holder -- tar czf - -C /must-gather . > "${OUTPUT_FILE}"
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
