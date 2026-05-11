#!/bin/bash
#
# Simulates the Konflux hermetic build process locally using Hermeto.
# Can build just the dependency cache, or a full container image, or both.
#
# Prerequisites: podman
#
# Examples:
#   ./hack/local-hermeto-build.sh -d . --no-image             # Cache only
#   ./hack/local-hermeto-build.sh -d . -i must-gather:test    # Cache + image
#   ./hack/local-hermeto-build.sh -d . -i must-gather:test --no-cache  # Image only (cache must exist)
#
# Cross-platform (requires qemu-user-static):
#   TARGET_PLATFORM=linux/arm64 ./hack/local-hermeto-build.sh -d . -i must-gather:test
set -euo pipefail

readonly LOCAL_CACHE_BASEDIR='./hermeto-cache/'
readonly HERMETO_IMAGE='quay.io/konflux-ci/hermeto:latest'

TARGET_PLATFORM="${TARGET_PLATFORM:-}"

normalize_arch() {
  local arch="$1"
  case "${arch}" in
    arm64)  echo "aarch64" ;;
    amd64)  echo "x86_64" ;;
    *)      echo "${arch}" ;;
  esac
}

get_target_arch() {
  if [[ -z "${TARGET_PLATFORM}" ]]; then
    normalize_arch "$(uname -m)"
    return
  fi
  local platform_arch="${TARGET_PLATFORM#*/}"
  normalize_arch "${platform_arch}"
}

TARGET_ARCH="$(get_target_arch)"

usage() {
  cat << EOF

Usage: Simulates the Konflux hermetic build by running Hermeto to prefetch
  dependencies, then building the container image with --network none.

Required:
  -d, --directory <path>   The directory of the component to build

Options:
  -i, --image <name>      Container image name (e.g., quay.io/example/image:tag)
                          Required to build image unless --no-image is specified
  --no-cache              Skip cache build (use existing cache)
  --no-image              Skip image build (only build cache)
  -h, --help              Show this help message

Environment variables:
  TARGET_PLATFORM         Target platform for podman (e.g., linux/arm64, linux/amd64).
                          If not set, builds for the native platform.
  BUILD_ARGS              Additional podman build arguments (e.g., --build-arg KEY=VAL).

Examples:
  $0 -d . --no-image                                # Build cache only
  $0 -d . -i quay.io/example/image:tag              # Build cache and image
  $0 -d . -i quay.io/example/image:tag --no-cache   # Image only (cache must exist)

Cross-platform build (ARM on x86), requires qemu-user-static:
  TARGET_PLATFORM=linux/arm64 $0 -d . -i quay.io/example/image:tag
EOF
  exit 1
}

build_cache() {
  local local_cache_dir="$1"
  local local_cache_output_dir="$2"
  local platform_args=()

  if [[ -n "${TARGET_PLATFORM}" ]]; then
    platform_args=("--platform" "${TARGET_PLATFORM}")
    echo "Building cache for platform: ${TARGET_PLATFORM} (arch: ${TARGET_ARCH})"
  fi

  mkdir -p "${local_cache_output_dir}"

  podman pull "${platform_args[@]}" "${HERMETO_IMAGE}"

  podman run --rm -ti \
    "${platform_args[@]}" \
    -v "${PWD}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    --log-level DEBUG \
    fetch-deps --dev-package-managers \
    --source . \
    --output /cachi2/output \
    '[{"type": "rpm", "path": "."}, {"type": "pip", "path": ".", "allow_binary": "false"}, {"type": "cargo", "path": "vendor/websocat"}]'

  podman run --rm -ti \
    "${platform_args[@]}" \
    -v "${PWD}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    generate-env --format env --output /cachi2/cachi2.env /cachi2/output

  podman run --rm -ti \
    "${platform_args[@]}" \
    -v "${PWD}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    inject-files /cachi2/output
}

build_image() {
  local component_dir="$1"
  local local_cache_dir="$2"
  local image="$3"
  local platform_args=()

  if [[ -n "${TARGET_PLATFORM}" ]]; then
    platform_args=("--platform" "${TARGET_PLATFORM}")
    echo "Building image for platform: ${TARGET_PLATFORM} (arch: ${TARGET_ARCH})"
  fi

  if [[ ! -d "${local_cache_dir}" ]]; then
    echo "Local cache dir does not exist. Please run the script without --no-cache first."
    echo "example: $0 -d ${component_dir} -i <image>"
    exit 1
  fi

  # Prevent podman from injecting host RHEL subscriptions into the container.
  # With --network none, dnf/microdnf fails trying to access these repos.
  EMPTY_DIR=$(mktemp -d)
  trap 'rm -rf "${EMPTY_DIR}"' EXIT

  # shellcheck disable=SC2086
  podman build -t "${image}" \
    "${platform_args[@]}" \
    --network none \
    --no-cache \
    ${BUILD_ARGS:-} \
    -f "${component_dir}/Containerfile" \
    -v "${local_cache_dir}:/cachi2" \
    -v /dev/null:/run/secrets/redhat.repo \
    -v "${EMPTY_DIR}:/run/secrets/rhsm:z" \
    -v "${EMPTY_DIR}:/run/secrets/etc-pki-entitlement:z" \
    "${component_dir}"
}

main() {
  local component_dir=""
  local image=""
  local no_cache=false
  local no_image=false

  while [[ $# -gt 0 ]]; do
    case "$1" in
      -d|--directory)
        if [[ -z "${2:-}" ]]; then
          echo "Error: -d/--directory requires a path argument" >&2
          usage
        fi
        component_dir="$2"
        shift 2
        ;;
      -i|--image)
        if [[ -z "${2:-}" ]]; then
          echo "Error: -i/--image requires an image name argument" >&2
          usage
        fi
        image="$2"
        shift 2
        ;;
      --no-cache)
        no_cache=true
        shift
        ;;
      --no-image)
        no_image=true
        shift
        ;;
      -h|--help)
        usage
        ;;
      *)
        echo "Error: Unknown option: $1" >&2
        usage
        ;;
    esac
  done

  if [[ -z "${component_dir}" ]]; then
    echo "Error: Directory is required. Use -d or --directory to specify." >&2
    usage
  fi

  if [[ "${no_cache}" == true && "${no_image}" == true ]]; then
    echo "Error: Nothing to do - both cache and image builds are disabled" >&2
    usage
  fi

  if [[ -z "${image}" ]]; then
    no_image=true
  fi

  mkdir -p "${LOCAL_CACHE_BASEDIR}"
  local resolved_component_dir
  local local_cache_dir
  local local_cache_output_dir

  resolved_component_dir="$(realpath "${component_dir}")"
  local_cache_dir="$(realpath "${LOCAL_CACHE_BASEDIR}")/$(basename "${resolved_component_dir}")"
  local_cache_output_dir="${local_cache_dir}/output"

  echo "Component dir: ${resolved_component_dir}"
  echo "Local cache dir: ${local_cache_dir}"

  if [[ "${no_cache}" == false ]]; then
    echo "Building cache..."
    build_cache "${local_cache_dir}" "${local_cache_output_dir}"
  else
    echo "Skipping cache build (--no-cache specified)"
  fi

  if [[ "${no_image}" == false ]]; then
    echo "Building image..."
    build_image "${resolved_component_dir}" "${local_cache_dir}" "${image}"
  else
    echo "Skipping image build (--no-image specified or -i/--image not provided)"
  fi
}

main "$@"
