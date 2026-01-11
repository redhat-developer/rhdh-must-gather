#!/usr/bin/env bats
# Integration tests for gather_orchestrator script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"
}

teardown() {
    teardown_test_environment
}

# ============================================================================
# Help and inclusion tests
# ============================================================================

@test "must_gather --help mentions orchestrator option" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--without-orchestrator" ]]
}

@test "must_gather --help describes orchestrator components" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "Serverless" ]] || [[ "$output" =~ "SonataFlowPlatform" ]]
}

# ============================================================================
# Script existence and executability tests
# ============================================================================

@test "gather_orchestrator script exists" {
    [ -f "${SCRIPTS_DIR}/gather_orchestrator" ]
}

@test "gather_orchestrator script is executable" {
    [ -x "${SCRIPTS_DIR}/gather_orchestrator" ]
}

# ============================================================================
# Script sourcing tests
# ============================================================================

@test "gather_orchestrator sources common.sh" {
    run grep -q "source.*common.sh" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator creates orchestrator directory" {
    run grep -q 'orchestrator_dir="\$BASE_COLLECTION_PATH/orchestrator"' "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Function existence tests
# ============================================================================

@test "gather_orchestrator defines gather_serverless_operators function" {
    run grep -q "function gather_serverless_operators" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator defines gather_orchestrator_crds function" {
    run grep -q "function gather_orchestrator_crds" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator defines gather_sonataflow_platforms function" {
    run grep -q "function gather_sonataflow_platforms" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator defines gather_knative_resources function" {
    run grep -q "function gather_knative_resources" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator defines generate_orchestrator_summary function" {
    run grep -q "function generate_orchestrator_summary" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# CRD collection tests
# ============================================================================

@test "gather_orchestrator collects SonataFlowPlatform CRDs" {
    run grep -q "sonataflowplatforms.sonataflow.org" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator collects SonataFlow CRDs" {
    run grep -q "sonataflows.sonataflow.org" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator collects KnativeServing CRDs" {
    run grep -q "knativeservings.operator.knative.dev" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator collects KnativeEventing CRDs" {
    run grep -q "knativeeventings.operator.knative.dev" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Namespace detection tests
# ============================================================================

@test "gather_orchestrator checks openshift-serverless namespace" {
    run grep -q "openshift-serverless" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator checks openshift-serverless-logic namespace" {
    run grep -q "openshift-serverless-logic" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator checks knative-serving namespace" {
    run grep -q "knative-serving" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator checks knative-eventing namespace" {
    run grep -q "knative-eventing" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Summary generation tests
# ============================================================================

@test "gather_orchestrator generates summary file" {
    run grep -q 'summary_file="\$orchestrator_dir/summary.txt"' "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}

@test "gather_orchestrator summary includes version information collection" {
    run grep -q "csv.*version" "${SCRIPTS_DIR}/gather_orchestrator"
    [ "$status" -eq 0 ]
}
