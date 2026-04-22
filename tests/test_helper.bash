#!/usr/bin/env bash
# Test helper functions for BATS tests

# Get the directory containing the tests
TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${TESTS_DIR}/.." && pwd)"
SCRIPTS_DIR="${PROJECT_ROOT}/collection-scripts"

# Create a temporary directory for test output
setup_test_environment() {
    export TEST_TMPDIR="$(mktemp -d)"
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"
    export LOG_LEVEL="${LOG_LEVEL:-error}"  # Suppress logs during tests unless debugging
    
    # Create required directories
    mkdir -p "${BASE_COLLECTION_PATH}"
}

# Cleanup temporary directory after tests
teardown_test_environment() {
    if [[ -n "${TEST_TMPDIR:-}" && -d "${TEST_TMPDIR}" ]]; then
        rm -rf "${TEST_TMPDIR}"
    fi
}

# Source the common.sh with test-friendly settings
load_common() {
    # Mock kubectl/oc commands if not available
    if ! command -v kubectl &>/dev/null && ! command -v oc &>/dev/null; then
        kubectl() {
            echo "mock-kubectl-output"
            return 0
        }
        oc() {
            echo "mock-oc-output"
            return 0
        }
        export -f kubectl
        export -f oc
    fi
    
    # Source common.sh
    source "${SCRIPTS_DIR}/common.sh"
    
    # Export functions for use in subshells (for BATS run command)
    export -f log
    export -f log_info
    export -f log_warn
    export -f log_error
    export -f log_success
    export -f log_debug
    export -f should_include_namespace
    export -f get_namespace_args
    export -f command_exists
    export -f ensure_directory
    export -f is_container
    export -f safe_exec
    export -f export_log_collection_args
    export -f run
    export -f get_kubectl_cmd
    export -f check_cluster_connectivity
    export -f validate_environment
    export -f collect_rhdh_workload
    export -f collect_rhdh_db_statefulset
}

# Create a mock secret YAML file for testing
create_mock_secret() {
    local output_file="$1"
    cat > "$output_file" << 'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: test-secret
  namespace: test-ns
type: Opaque
data:
  password: c2VjcmV0cGFzc3dvcmQxMjM= # notsecret
  api-key: YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo= # notsecret
  token: ZXlKaGJHY2lPaUpJVXpJMU5pSXNJblI1Y0NJNklrcFhWQ0o5LmV5SnpkV0lpT2lJeE1qTTBOVFkzT0Rrd0lpd2libUZ0WlNJNklrcHZhRzRnUkc5bElpd2lhV0YwSWpveE5URTJNak01TURJeWZRLlNmbEt4d1JKU01lS0tGMlFUNGZ3cE1lSmYzNmVvdFdoYVljV0hfTF9WZ00= # notsecret
EOF
}

# Create a mock log file with sensitive data
create_mock_log_with_secrets() {
    local output_file="$1"
    cat > "$output_file" << 'EOF'
2024-01-15 10:30:00 INFO Starting application
2024-01-15 10:30:01 DEBUG authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36eoTWhAYCWH_L_VgM # notsecret
2024-01-15 10:30:02 INFO Connection established
2024-01-15 10:30:03 DEBUG Database URL: postgres://user:password=secretpass123&host=db.example.com
2024-01-15 10:30:04 INFO Request completed
EOF
}

# Create a mock ConfigMap YAML
create_mock_configmap() {
    local output_file="$1"
    cat > "$output_file" << 'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-configmap
  namespace: test-ns
data:
  config.yaml: |
    database:
      host: localhost
      port: 5432
    server:
      port: 8080
EOF
}

# Create mock platform detection data
create_mock_k8s_version_output() {
    cat << 'EOF'
{
  "clientVersion": {
    "major": "1",
    "minor": "30",
    "gitVersion": "v1.30.0"
  },
  "serverVersion": {
    "major": "1",
    "minor": "29",
    "gitVersion": "v1.29.2"
  }
}
EOF
}

# Assert file contains string (uses fixed string matching, not regex)
assert_file_contains() {
    local file="$1"
    local expected="$2"
    if ! grep -F -q "$expected" "$file"; then
        echo "Expected file '$file' to contain '$expected'"
        echo "File contents:"
        cat "$file"
        return 1
    fi
}

# Assert file does not contain string (uses fixed string matching, not regex)
assert_file_not_contains() {
    local file="$1"
    local unexpected="$2"
    if grep -F -q "$unexpected" "$file"; then
        echo "Expected file '$file' to NOT contain '$unexpected'"
        echo "File contents:"
        cat "$file"
        return 1
    fi
}

# Assert directory exists
assert_dir_exists() {
    local dir="$1"
    if [[ ! -d "$dir" ]]; then
        echo "Expected directory '$dir' to exist"
        return 1
    fi
}

# Assert file exists
assert_file_exists() {
    local file="$1"
    if [[ ! -f "$file" ]]; then
        echo "Expected file '$file' to exist"
        return 1
    fi
}

# Assert command succeeds
assert_success() {
    if [[ "$status" -ne 0 ]]; then
        echo "Expected command to succeed, but it failed with status $status"
        echo "Output: $output"
        return 1
    fi
}

# Assert command fails
assert_failure() {
    if [[ "$status" -eq 0 ]]; then
        echo "Expected command to fail, but it succeeded"
        echo "Output: $output"
        return 1
    fi
}
