#!/bin/bash
# Common utilities for E2E tests
# This file is sourced by all E2E test scripts.

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Global error counter - each script should reset this before running checks
ERRORS=0

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Dump file content to stderr for debugging failed assertions.
# Truncates to first and last 50 lines if the file is large.
_dump_file_context() {
    local file="$1"
    if [ ! -e "$file" ]; then
        log_error "  ↳ File does not exist: $file"
        local parent
        parent="$(dirname "$file")"
        if [ -d "$parent" ]; then
            log_error "  ↳ Parent directory contents ($(basename "$parent")/):"
            # shellcheck disable=SC2012
            ls -la "$parent" | while IFS= read -r line; do
                log_error "      $line"
            done
        else
            log_error "  ↳ Parent directory also missing: $parent"
        fi
        return
    fi
    if [ ! -s "$file" ]; then
        log_error "  ↳ File exists but is empty (0 bytes): $file"
        return
    fi
    local total_lines
    total_lines=$(wc -l < "$file")
    local max_lines=100
    log_error "  ↳ File content ($total_lines lines):"
    if [ "$total_lines" -le "$max_lines" ]; then
        while IFS= read -r line; do
            log_error "      $line"
        done < "$file"
    else
        log_error "      --- first 50 lines ---"
        head -n 50 "$file" | while IFS= read -r line; do
            log_error "      $line"
        done
        log_error "      --- ... truncated $(( total_lines - 100 )) lines ... ---"
        log_error "      --- last 50 lines ---"
        tail -n 50 "$file" | while IFS= read -r line; do
            log_error "      $line"
        done
    fi
}

# Dump directory listing for debugging failed assertions.
_dump_dir_context() {
    local dir="$1"
    if [ ! -e "$dir" ]; then
        log_error "  ↳ Directory does not exist: $dir"
        local parent
        parent="$(dirname "$dir")"
        if [ -d "$parent" ]; then
            log_error "  ↳ Parent directory contents ($(basename "$parent")/):"
            # shellcheck disable=SC2012
            ls -la "$parent" | while IFS= read -r line; do
                log_error "      $line"
            done
        else
            log_error "  ↳ Parent directory also missing: $parent"
        fi
        return
    fi
    log_error "  ↳ Directory listing:"
    # shellcheck disable=SC2012
    ls -la "$dir" | while IFS= read -r line; do
        log_error "      $line"
    done
}

# Detect if we're running on an OpenShift cluster
is_openshift() {
    # Check if the cluster has OpenShift-specific API resources
    kubectl api-resources --api-group=config.openshift.io 2>/dev/null | grep -q clusterversion
}

# Validation helper functions
check_file_exists() {
    local file="$1"
    local description="$2"
    if [ -f "$file" ]; then
        log_info "✓ Found $description: $file"
    else
        log_error "✗ Missing $description: $file"
        _dump_file_context "$file"
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
        _dump_dir_context "$dir"
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
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_file_valid_json() {
    local file="$1"
    local description="$2"
    check_file_exists "$file" "$description"
    if ! jq . "$file" >/dev/null 2>&1; then
        log_error "✗ $description is not valid JSON: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_dir_not_empty() {
    local dir="$1"
    local description="$2"
    check_dir_exists "$dir" "$description"
    if [ -d "$dir" ] && [ -n "$(ls -A "$dir" 2>/dev/null)" ]; then
        log_info "✓ Found non-empty $description: $dir"
    else
        log_error "✗ $description is empty"
        _dump_dir_context "$dir"
        ((ERRORS++))
    fi
}

check_file_contains() {
    local file="$1"
    local content="$2"
    local description="$3"
    check_file_exists "$file" "$description"
    if [ -f "$file" ] && grep -q "$content" "$file"; then
        log_info "✓ Found $content in $file"
    else
        log_error "✗ $description does not contain '$content': $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

check_file_no_error() {
    local file="$1"
    local description="$2"
    if [ -f "$file" ] && grep -qiE 'Error from server|NotFound|forbidden|unknown command|Command failed or timed out' "$file"; then
        log_error "✗ $description contains error output: $file"
        _dump_file_context "$file"
        ((ERRORS++))
    fi
}

# Returns the current error count
get_error_count() {
    echo "$ERRORS"
}

# Resets the error counter
reset_errors() {
    ERRORS=0
}

# OCI Helm chart published by RHDH midstream (Konflux CI tags: X.Y-N-CI).
# Also used by run-e2e-tests.sh after sourcing this file.
HELM_CHART_OCI_REF="oci://quay.io/rhdh/chart"
HELM_CHART_SKOPEO_REF="${HELM_CHART_OCI_REF/oci:/docker:}"

# Resolve chart major.minor (X.Y) from must-gather E2E target branch.
# main: highest X.Y present on the chart registry (matches rhdh CI helm::get_chart_major_version).
# release-x.y: x.y from the branch name.
chart_major_version_for_target_branch() {
    local branch="$1"
    case "$branch" in
        main)
            if ! command -v skopeo &>/dev/null; then
                log_error "skopeo is required to resolve Helm chart version for main"
                return 1
            fi
            skopeo list-tags "$HELM_CHART_SKOPEO_REF" 2>/dev/null | \
                jq -r '.Tags[]' | \
                grep -oE '^[0-9]+\.[0-9]+' | \
                sort -t. -k1,1n -k2,2n -u | \
                tail -1
            ;;
        release-*)
            echo "${branch#release-}"
            ;;
        *)
            log_error "Unsupported target branch for chart resolution: $branch"
            return 1
            ;;
    esac
}

# Pick the newest X.Y-N-CI tag for major.minor X.Y from a list of tag names (stdin).
# Uses version sort so 2.0-100-CI ranks above 2.0-11-CI.
select_latest_ci_chart_version_from_tags() {
    local major="$1"
    grep -E "^${major}-[0-9]+-CI$" | sort -V | tail -1
}

# Latest Konflux CI chart tag for major.minor X.Y from the chart registry.
latest_ci_chart_version_for_major() {
    local major="$1"
    if ! command -v skopeo &>/dev/null; then
        log_error "skopeo is required to resolve Helm chart version"
        return 1
    fi
    skopeo list-tags "$HELM_CHART_SKOPEO_REF" 2>/dev/null | \
        jq -r '.Tags[]' | \
        select_latest_ci_chart_version_from_tags "$major"
}

# Extract chart major.minor (X.Y) from a Konflux CI tag (e.g. 2.0-59-CI -> 2.0).
chart_major_from_version() {
    echo "${1%%-*}"
}

# Render a Helm chart to stdout, omitting Helm 4 OCI pull status lines.
# Helm 4 prints "Pulled:" / "Digest:" to stdout for oci:// charts; kubectl apply rejects those as manifests.
helm_template_yaml() {
    helm template "$@" | sed '/^Pulled:/d; /^Digest:/d'
}

# Append SIGUSR2 heap-dump env vars to a Helm values file (must preserve chart defaults).
append_heap_dump_sigusr2_values() {
    local outfile="$1"
    cat >> "$outfile" <<'EOF'
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
}

# Kind-friendly PostgreSQL image (registry.redhat.io requires authenticated pull).
write_standalone_postgresql_values() {
    local outfile="$1"
    cat >> "$outfile" <<'EOF'
upstream:
  postgresql:
    enabled: true
    image:
      registry: quay.io
      repository: fedora/postgresql-15
      tag: latest
    primary:
      podSecurityContext:
        enabled: false
      containerSecurityContext:
        enabled: false
EOF
}

# Write Helm values for E2E deployments keyed by chart major (1.x vs 2.x), not git branch name.
# mode: misconfigured (native helm, expects CreateContainerConfigError) | standalone (running pod)
write_helm_e2e_values() {
    local chart_major="$1"
    local outfile="$2"
    local mode="$3"

    case "$chart_major" in
        1.*)
            case "$mode" in
                misconfigured)
                    cat > "$outfile" <<EOF
route:
  enabled: false
upstream:
  backstage:
    replicas: 2
  postgresql:
    # Purposely disable the local database to simulate a misconfigured application (missing external database info)
    enabled: false
global:
  # TODO(asoro): RHDHBUGS-3095: remove this pin once the ghcr.io reference issue is fixed
  catalogIndex:
    image:
      tag: "1.10-51"
  lightspeed:
    enabled: false
  dynamic:
    includes: []
EOF
                    ;;
                standalone)
                    cat > "$outfile" <<EOF
route:
  enabled: false
global:
  catalogIndex:
    image:
      tag: "1.10-51"
  dynamic:
    includes:
      - dynamic-plugins.default.yaml
EOF
                    write_standalone_postgresql_values "$outfile"
                    ;;
                *)
                    log_error "Unknown Helm E2E values mode: $mode"
                    return 1
                    ;;
            esac
            ;;
        2.*)
            case "$mode" in
                misconfigured)
                    cat > "$outfile" <<EOF
route:
  enabled: false
upstream:
  backstage:
    replicas: 2
  postgresql:
    enabled: false
global:
  lightspeed:
    enabled: false
  dynamic:
    includes: []
EOF
                    ;;
                standalone)
                    cat > "$outfile" <<EOF
route:
  enabled: false
global:
  dynamic:
    includes:
      - dynamic-plugins.default.yaml
EOF
                    write_standalone_postgresql_values "$outfile"
                    ;;
                *)
                    log_error "Unknown Helm E2E values mode: $mode"
                    return 1
                    ;;
            esac
            ;;
        *)
            log_error "Unsupported chart major for E2E values: $chart_major"
            return 1
            ;;
    esac
}

# Wait until misconfigured Helm pods finish init and backstage-backend hits CreateContainerConfigError.
# Tolerates transient kubectl failures under the caller's set -euo pipefail.
wait_for_helm_misconfigured_backstage_pods() {
    local namespace="$1"
    local release="$2"
    local expected_count="${3:-2}"
    local timeout="${4:-600}"
    local selector="app.kubernetes.io/instance=${release},app.kubernetes.io/component=backstage"
    local deadline=$((SECONDS + timeout))

    while [ "$SECONDS" -lt "$deadline" ]; do
        local pods count
        pods=$(kubectl -n "$namespace" get pods -l "$selector" \
            -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
        count=$(printf '%s' "$pods" | wc -w)
        if [ "$count" -ge "$expected_count" ]; then
            break
        fi
        sleep 2
    done

    local pod_count pods_final
    pods_final=$(kubectl -n "$namespace" get pods -l "$selector" \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
    pod_count=$(printf '%s' "$pods_final" | wc -w)
    if [ "$pod_count" -lt "$expected_count" ]; then
        log_error "Could not find ${expected_count} Helm-deployed RHDH pods in namespace ${namespace}."
        return 1
    fi

    log_info "Found Helm pods: ${pods_final}"

    while [ "$SECONDS" -lt "$deadline" ]; do
        local all_ready=true
        local pod pod_names
        pod_names=$(kubectl -n "$namespace" get pods -l "$selector" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)

        while IFS= read -r pod; do
            [ -z "$pod" ] && continue

            local pod_json
            pod_json=$(kubectl -n "$namespace" get pod "$pod" -o json 2>/dev/null || true)
            if [ -z "$pod_json" ]; then
                all_ready=false
                continue
            fi

            local unfinished_inits
            unfinished_inits=$(printf '%s' "$pod_json" | \
                jq '[.status.initContainerStatuses[]? | select(.state.terminated == null)] | length')

            if [ "${unfinished_inits:-0}" -gt 0 ]; then
                all_ready=false
                continue
            fi

            local init_pull_errors
            init_pull_errors=$(printf '%s' "$pod_json" | \
                jq -r '[.status.initContainerStatuses[]?.state.waiting.reason // empty] | map(select(. == "ErrImagePull" or . == "ImagePullBackOff" or . == "CrashLoopBackOff")) | length')
            if [ "${init_pull_errors:-0}" -gt 0 ]; then
                log_error "Init container failed on pod ${pod} (image pull or crash)."
                kubectl describe pod "$pod" -n "$namespace" >&2 || true
                return 1
            fi

            local reason
            reason=$(printf '%s' "$pod_json" | \
                jq -r '.status.containerStatuses[]? | select(.name=="backstage-backend") | .state.waiting.reason // empty')
            if [ "$reason" != "CreateContainerConfigError" ]; then
                all_ready=false
            fi
        done <<< "$pod_names"

        if [ "$all_ready" = true ]; then
            return 0
        fi
        sleep 2
    done

    log_error "Helm-deployed pods did not reach CreateContainerConfigError on backstage-backend within expected time."
    return 1
}
