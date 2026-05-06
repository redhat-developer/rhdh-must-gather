#!/usr/bin/env bats
# Tests for gather_namespace-inspect script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"
}

teardown() {
    teardown_test_environment
}

# ============================================================================
# Script existence and basic tests
# ============================================================================

@test "gather_namespace-inspect script exists" {
    [ -f "${SCRIPTS_DIR}/gather_namespace-inspect" ]
}

@test "gather_namespace-inspect script is executable" {
    [ -x "${SCRIPTS_DIR}/gather_namespace-inspect" ]
}

@test "gather_namespace-inspect sources common.sh" {
    run grep -q "source.*common.sh" "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Orchestrator namespace integration tests
# ============================================================================

@test "gather_namespace-inspect reads orchestrator detected-namespaces.txt" {
    run grep -q 'orchestrator/detected-namespaces.txt' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

@test "gather_namespace-inspect adds orchestrator namespaces to inspection" {
    run grep -q 'Adding orchestrator-related namespaces' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

@test "gather_namespace-inspect deduplicates orchestrator namespaces" {
    run grep -q 'already_included=true' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

@test "gather_namespace-inspect handles empty orchestrator namespaces file" {
    # The -s test returns false for empty files, so the block is skipped
    run grep -q '\-s "\$orch_ns_file"' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

@test "gather_namespace-inspect exits when no namespaces found" {
    run grep -q 'No RHDH deployments or orchestrator components found' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
}

@test "gather_namespace-inspect only adds orchestrator namespaces when auto-detecting" {
    run grep -B1 'orch_ns_file=' "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
    [[ "$output" =~ '-z "${RHDH_TARGET_NAMESPACES:-}"' ]]
}
