#!/bin/bash
#
# Copyright Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Simulates the Konflux build process locally using Hermeto.
set -euo pipefail

readonly LOCAL_CACHE_BASEDIR='/tmp/hermeto-cache'
readonly HERMETO_IMAGE='quay.io/konflux-ci/hermeto:0.60.1'
readonly HERMETIC_CONTAINERFILE='.rhdh/docker/Containerfile'

TARGET_PLATFORM="${TARGET_PLATFORM:-}"

usage() {
  cat << EOF

Usage: Build a hermeto dependency cache and/or a must-gather image with --network none.

Required:
  -d, --directory <path>   Repository root (must contain go.mod and rpms.lock.yaml)

Options:
  -i, --image <name>       Image reference (e.g., localhost/rhdh-must-gather:test)
  --no-cache               Skip cache build (use existing cache)
  --no-image               Skip image build (only build cache)
  -h, --help               Show this help message

Environment variables:
  TARGET_PLATFORM          Target platform for podman (e.g., linux/arm64, linux/amd64)

Examples:
  $0 -d . --no-image
  $0 -d . -i localhost/rhdh-must-gather:hermetic-test
  TARGET_PLATFORM=linux/arm64 $0 -d . -i localhost/rhdh-must-gather:arm64
EOF
  exit 1
}

build_cache() {
  local component_dir="$1"
  local local_cache_dir="$2"
  local platform_args=()

  if [[ -n "${TARGET_PLATFORM}" ]]; then
    platform_args=("--platform" "${TARGET_PLATFORM}")
  fi

  mkdir -p "${local_cache_dir}/output"

  podman pull "${platform_args[@]}" "${HERMETO_IMAGE}"

  podman run --rm \
    "${platform_args[@]}" \
    -v "${component_dir}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    --log-level DEBUG \
    fetch-deps \
    --source . \
    --output /cachi2/output \
    '[{"type": "rpm", "path": "."}, {"type": "gomod", "path": "."}]'

  podman run --rm \
    "${platform_args[@]}" \
    -v "${component_dir}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    generate-env --format env --output /cachi2/cachi2.env /cachi2/output

  podman run --rm \
    "${platform_args[@]}" \
    -v "${component_dir}:/source:z" \
    -v "${local_cache_dir}:/cachi2:z" \
    -w /source \
    "${HERMETO_IMAGE}" \
    inject-files /cachi2/output
}

build_image() {
  local component_dir="$1"
  local local_cache_dir="$2"
  local image="$3"
  local version="${4:-0.0.0-local}"
  local platform_args=()

  if [[ -n "${TARGET_PLATFORM}" ]]; then
    platform_args=("--platform" "${TARGET_PLATFORM}")
  fi

  if [[ ! -d "${local_cache_dir}/output" ]]; then
    echo "Local cache dir does not exist. Run without --no-cache first." >&2
    exit 1
  fi

  EMPTY_DIR=$(mktemp -d)
  trap 'rm -rf "${EMPTY_DIR}" || true' EXIT

  podman build -t "${image}" \
    "${platform_args[@]}" \
    --network none \
    --build-arg "RHDH_MUST_GATHER_VERSION=${version}" \
    -f "${component_dir}/${HERMETIC_CONTAINERFILE}" \
    -v "${local_cache_dir}:/cachi2:z" \
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
        component_dir="$2"
        shift 2
        ;;
      -i|--image)
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
    echo "Error: -d/--directory is required" >&2
    usage
  fi

  if [[ "${no_cache}" == true && "${no_image}" == true ]]; then
    echo "Error: Nothing to do - both cache and image builds are disabled" >&2
    exit 1
  fi

  if [[ -z "${image}" ]]; then
    no_image=true
  fi

  local resolved_component_dir
  local local_cache_dir

  resolved_component_dir="$(realpath "${component_dir}")"
  local_cache_dir="${LOCAL_CACHE_BASEDIR}/rhdh-must-gather"
  mkdir -p "${LOCAL_CACHE_BASEDIR}"

  if [[ ! -f "${resolved_component_dir}/${HERMETIC_CONTAINERFILE}" ]]; then
    echo "Error: ${HERMETIC_CONTAINERFILE} not found under ${resolved_component_dir}" >&2
    exit 1
  fi

  echo "Component dir: ${resolved_component_dir}"
  echo "Local cache dir: ${local_cache_dir}"

  if [[ "${no_cache}" == false ]]; then
    echo "Building cache..."
    build_cache "${resolved_component_dir}" "${local_cache_dir}"
  else
    echo "Skipping cache build (--no-cache specified)"
  fi

  if [[ "${no_image}" == false ]]; then
    echo "Building image..."
    build_image "${resolved_component_dir}" "${local_cache_dir}" "${image}" "0.0.0-local"
  else
    echo "Skipping image build"
  fi
}

main "$@"
