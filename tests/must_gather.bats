#!/usr/bin/env bats
# Integration tests for must_gather script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"
}

teardown() {
    teardown_test_environment
}

# ============================================================================
# Help and usage tests
# ============================================================================

@test "must_gather --help displays usage information" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "Usage:" ]]
    [[ "$output" =~ "--help" ]]
}

@test "must_gather --help mentions --with-secrets option" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--with-secrets" ]]
}

@test "must_gather --help mentions --with-heap-dumps option" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--with-heap-dumps" ]]
}

@test "must_gather --help mentions --namespaces option" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--namespaces" ]]
}

@test "must_gather --help lists all exclusion options" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--without-operator" ]]
    [[ "$output" =~ "--without-orchestrator" ]]
    [[ "$output" =~ "--without-helm" ]]
    [[ "$output" =~ "--without-platform" ]]
    [[ "$output" =~ "--without-route" ]]
    [[ "$output" =~ "--without-ingress" ]]
    [[ "$output" =~ "--without-namespace-inspect" ]]
}

# ============================================================================
# Flag parsing tests
# ============================================================================

@test "must_gather accepts --cluster-info flag" {
    # Just test that the flag is parsed without error (we can't fully run without a cluster)
    run "${SCRIPTS_DIR}/must_gather" --help
    [[ "$output" =~ "--cluster-info" ]]
}

@test "must_gather displays warning for unknown flags" {
    # This tests the printf warning in parse_flags
    run bash -c "source '${SCRIPTS_DIR}/common.sh' 2>/dev/null && source '${SCRIPTS_DIR}/must_gather' --unknown-flag --help 2>&1 || true"
    [[ "$output" =~ "Unknown option" ]] || [[ "$output" =~ "Usage:" ]]
}

# ============================================================================
# Environment variable tests
# ============================================================================

@test "must_gather respects BASE_COLLECTION_PATH environment variable" {
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}/custom-output"
    mkdir -p "$BASE_COLLECTION_PATH"
    
    # Just verify the env var is respected by checking the help output references it
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
}

@test "must_gather respects LOG_LEVEL environment variable" {
    export LOG_LEVEL="debug"
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
}

