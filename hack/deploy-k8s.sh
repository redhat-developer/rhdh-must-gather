#!/usr/bin/env bash
#
# Run RHDH must-gather on a standard Kubernetes cluster using Kustomize.
#
# Usage:
#   ./hack/deploy-k8s.sh [OPTIONS]
#
# Options:
#   --image <image>     Full image name (default: quay.io/rhdh-community/rhdh-must-gather:latest)
#   --overlay <overlay> Overlay to use. Can be:
#                       - A pre-built overlay name (e.g., "with-heap-dumps", "debug-mode")
#                       - A full/relative path to a user-defined overlay directory
#   --opts <options>    Additional options to pass to the gather script (quote multiple options)
#   --output <file>     Output file path (default: rhdh-must-gather-output.k8s.<timestamp>.tar.gz)
#   --help              Show this help message
#
# Examples:
#   ./hack/deploy-k8s.sh
#   ./hack/deploy-k8s.sh --image quay.io/myorg/rhdh-must-gather:v1.0.0
#   ./hack/deploy-k8s.sh --overlay with-heap-dumps
#   ./hack/deploy-k8s.sh --overlay debug-mode --opts "--namespaces my-ns"
#   ./hack/deploy-k8s.sh --overlay /path/to/my-overlay
#   ./hack/deploy-k8s.sh --output ./debug-mustgather.tar.gz
#   ./hack/deploy-k8s.sh --image myimage:tag --overlay with-heap-dumps --opts "--with-secrets --namespaces my-ns"
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
KUSTOMIZE_BASE="${REPO_ROOT}/deploy"

# Default values
DEFAULT_IMAGE="quay.io/rhdh-community/rhdh-must-gather:latest"
IMAGE="${DEFAULT_IMAGE}"
OVERLAY=""
OPTS_STRING=""
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
        --overlay)
            OVERLAY="$2"
            shift 2
            ;;
        --opts)
            OPTS_STRING="$2"
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

# Convert OPTS_STRING to array
OPTS=()
if [[ -n "${OPTS_STRING}" ]]; then
    read -ra OPTS <<< "${OPTS_STRING}"
fi

# Extract image components
IMAGE_NAME="${IMAGE%:*}"
IMAGE_TAG="${IMAGE##*:}"
if [[ "${IMAGE_TAG}" == "${IMAGE_NAME}" ]]; then
    IMAGE_TAG="latest"
fi

# Resolve overlay path
OVERLAY_PATH=""
if [[ -n "${OVERLAY}" ]]; then
    if [[ -d "${OVERLAY}" ]]; then
        # Full/relative path to a user-defined overlay
        OVERLAY_PATH="$(cd "${OVERLAY}" && pwd)"
    elif [[ -d "${KUSTOMIZE_BASE}/overlays/${OVERLAY}" ]]; then
        # Pre-built overlay name
        OVERLAY_PATH="${KUSTOMIZE_BASE}/overlays/${OVERLAY}"
    else
        echo "Error: Overlay not found: ${OVERLAY}"
        echo "       Looked for:"
        echo "         - ${OVERLAY} (as path)"
        echo "         - ${KUSTOMIZE_BASE}/overlays/${OVERLAY} (as pre-built overlay)"
        exit 1
    fi
fi

# Generate unique namespace and output file (if not overridden)
TIMESTAMP=$(date +%s)
NAMESPACE="rhdh-must-gather-${TIMESTAMP}"
if [[ -z "${OUTPUT_FILE}" ]]; then
    OUTPUT_FILE="rhdh-must-gather-output.k8s.${TIMESTAMP}.tar.gz"
elif [[ "${OUTPUT_FILE}" != *.tar.gz ]]; then
    OUTPUT_FILE="${OUTPUT_FILE}.tar.gz"
fi

# Create temporary overlay directory
TMP_OVERLAY=$(mktemp -d)
trap 'rm -rf "${TMP_OVERLAY}"' EXIT

echo "Testing against a regular K8s cluster..."
echo ""

# Check for kubectl
if ! command -v kubectl &>/dev/null; then
    echo "Error: kubectl command not found."
    exit 1
fi

echo "Preparing must-gather resources in namespace: ${NAMESPACE}"
if [[ -n "${OVERLAY_PATH}" ]]; then
    echo "Using overlay: ${OVERLAY_PATH}"
fi

# Create symlink to base/overlay directory (Kustomize requires relative paths)
if [[ -n "${OVERLAY_PATH}" ]]; then
    ln -s "${OVERLAY_PATH}" "${TMP_OVERLAY}/base"
else
    ln -s "${KUSTOMIZE_BASE}" "${TMP_OVERLAY}/base"
fi

# Generate kustomization.yaml
cat > "${TMP_OVERLAY}/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: ${NAMESPACE}

resources:
  - base

images:
  - name: quay.io/rhdh-community/rhdh-must-gather
    newName: ${IMAGE_NAME}
    newTag: ${IMAGE_TAG}

EOF

# Add args patch if OPTS provided
if [[ ${#OPTS[@]} -gt 0 ]]; then
    cat >> "${TMP_OVERLAY}/kustomization.yaml" <<EOF
patches:
  - target:
      kind: Job
      name: rhdh-must-gather
    patch: |
      - op: add
        path: /spec/template/spec/containers/0/args
        value:
EOF
    for opt in "${OPTS[@]}"; do
        echo "          - \"${opt}\"" >> "${TMP_OVERLAY}/kustomization.yaml"
    done
fi

# Create resources
echo "Creating must-gather resources using this Kustomization overlay (${TMP_OVERLAY}/)..."
echo "---"
cat "${TMP_OVERLAY}/kustomization.yaml"
echo "---"
echo ""
kubectl apply -k "${TMP_OVERLAY}"
echo ""

# Wait for job completion
echo "Waiting for job to complete (timeout: 600s)..."
if ! kubectl -n "${NAMESPACE}" wait --for=condition=complete job/rhdh-must-gather --timeout=600s 2>&1; then
    echo "Error: Job did not complete within timeout"
    echo ""
    echo "Job logs:"
    kubectl -n "${NAMESPACE}" logs job/rhdh-must-gather --tail=50 || true
    exit 1
fi
echo "Job completed successfully"
echo ""

# Wait for data retriever pod
echo "Waiting for data retriever pod to be ready (timeout: 60s)..."
if ! kubectl -n "${NAMESPACE}" wait --for=condition=ready pod/rhdh-must-gather-data-retriever --timeout=60s 2>&1; then
    echo "Error: Data retriever pod did not become ready within timeout"
    echo ""
    kubectl -n "${NAMESPACE}" describe pod/rhdh-must-gather-data-retriever || true
    exit 1
fi
echo "Data retriever pod is ready"
echo ""

# Pull data
echo "Pulling must-gather data from pod..."
kubectl -n "${NAMESPACE}" exec rhdh-must-gather-data-retriever -- tar czf - -C /data . > "${OUTPUT_FILE}"
echo ""

# Cleanup
echo "Cleaning up resources..."
kubectl delete -k "${TMP_OVERLAY}" --wait=false 2>/dev/null || true
echo ""

echo "✓ Must-gather data saved to: ${OUTPUT_FILE}"
echo ""
echo "To extract the data, run:"
echo "  tar xzf ${OUTPUT_FILE}"

