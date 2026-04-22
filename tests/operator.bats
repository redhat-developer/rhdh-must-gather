#!/usr/bin/env bats
# shellcheck disable=SC2016 # Single quotes are intentional: grep patterns match literal $var in source files
# Unit tests for gather_operator script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"
}

teardown() {
    teardown_test_environment
}

# ============================================================================
# Script existence and executability tests
# ============================================================================

@test "gather_operator script exists" {
    [ -f "${SCRIPTS_DIR}/gather_operator" ]
}

@test "gather_operator script is executable" {
    [ -x "${SCRIPTS_DIR}/gather_operator" ]
}

@test "gather_operator sources common.sh" {
    run grep -q "source.*common.sh" "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Dual workload detection tests
# ============================================================================

@test "gather_operator checks for both Deployment and StatefulSet with same name" {
    run grep -qF 'get deployment "$deploy"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
    run grep -qF 'get statefulset "$deploy"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator detects dual workload condition" {
    run grep -q 'has_deploy.*&&.*has_sts' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator reads spec.deployment.kind from Backstage CR" {
    run grep -q "spec.deployment.kind" "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator writes warning-dual-workload.txt at CR directory level" {
    run grep -qF 'warning-dual-workload.txt' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator uses collect_rhdh_workload for each workload type in dual mode" {
    run grep -qF 'collect_rhdh_workload "$cr_ns" "$deploy" "deployment"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
    run grep -qF 'collect_rhdh_workload "$cr_ns" "$deploy" "statefulset"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator uses collect_rhdh_db_statefulset for database collection" {
    run grep -qF 'collect_rhdh_db_statefulset "$cr_ns" "$statefulset" "$cr_dir"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator does not use collect_rhdh_data" {
    run grep -qF 'collect_rhdh_data' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -ne 0 ]
}

@test "gather_operator collects StatefulSet data separately when dual workload detected" {
    run grep -qF 'rhdh-statefulset' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator identifies leftover kind based on spec.deployment.kind" {
    run grep -q 'intended_kind.*StatefulSet' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
    run grep -q 'leftover_kind="Deployment"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
    run grep -q 'leftover_kind="StatefulSet"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Collection structure tests
# ============================================================================

@test "gather_operator creates operator directory" {
    run grep -qF 'BASE_COLLECTION_PATH/operator' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator collects OLM information" {
    run grep -q 'gather_olm_information' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator collects CRDs" {
    run grep -q 'gather_crds' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator collects Backstage CRs" {
    run grep -q 'gather_backstage_crs' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "gather_operator uses backstage-psql naming convention for database" {
    run grep -qF 'backstage-psql-$cr_name' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}

@test "collect_rhdh_workload function exists in common.sh" {
    run grep -q '^collect_rhdh_workload()' "${SCRIPTS_DIR}/common.sh"
    [ "$status" -eq 0 ]
}

@test "collect_rhdh_workload accepts kind parameter for targeted collection" {
    # The function should use the kind parameter directly (no || fallback)
    run grep -A 5 'collect_rhdh_workload()' "${SCRIPTS_DIR}/common.sh"
    [ "$status" -eq 0 ]
    [[ "$output" =~ 'kind="$3"' ]]
}

@test "collect_rhdh_db_statefulset function exists in common.sh" {
    run grep -q '^collect_rhdh_db_statefulset()' "${SCRIPTS_DIR}/common.sh"
    [ "$status" -eq 0 ]
}

@test "collect_rhdh_data function no longer exists in common.sh" {
    run grep -q '^collect_rhdh_data()' "${SCRIPTS_DIR}/common.sh"
    [ "$status" -ne 0 ]
}

@test "gather_operator detects workload kind for single-workload case" {
    run grep -qF 'workload_kind="deployment"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
    run grep -qF 'workload_kind="statefulset"' "${SCRIPTS_DIR}/gather_operator"
    [ "$status" -eq 0 ]
}
