#!/usr/bin/env bats
# Unit tests for gather_helm script

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

@test "must_gather --help mentions helm option" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "--without-helm" ]]
}

# ============================================================================
# Script existence and executability tests
# ============================================================================

@test "gather_helm script exists" {
    [ -f "${SCRIPTS_DIR}/gather_helm" ]
}

@test "gather_helm script is executable" {
    [ -x "${SCRIPTS_DIR}/gather_helm" ]
}

# ============================================================================
# Script sourcing tests
# ============================================================================

@test "gather_helm sources common.sh" {
    run grep -q "source.*common.sh" "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm creates helm directory" {
    run grep -qF 'BASE_COLLECTION_PATH/helm' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

# ============================================================================
# RHDH pattern tests
# ============================================================================

@test "gather_helm defines RHDH_PATTERN for chart matching" {
    run grep -q 'RHDH_PATTERN="backstage|rhdh|developer-hub"' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm defines RHDH_IMAGE_PATTERN for container image matching" {
    run grep -q 'RHDH_IMAGE_PATTERN=' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

# ============================================================================
# Phase 1: Native Helm release detection tests
# ============================================================================

@test "gather_helm detects native Helm releases via helm list" {
    run grep -q 'helm list' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm filters native releases by RHDH patterns" {
    run grep -q 'backstage|rhdh|developer-hub' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm excludes must-gather from native helm text output" {
    run grep -q "grep -v 'must-gather'" "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm excludes must-gather from native helm JSON output" {
    run grep 'all-rhdh-releases.json' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
    [[ "$output" =~ test\(\"must-gather\" ]]
}

# ============================================================================
# Phase 2: Standalone Helm deployment detection tests
# ============================================================================

@test "gather_helm detects standalone Helm deployments" {
    run grep -q 'standalone_deployments=' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm detects standalone Helm statefulsets" {
    run grep -q 'standalone_statefulsets=' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm queries Helm-managed Deployments via label selector" {
    run grep -q 'app.kubernetes.io/managed-by=Helm' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm checks helm.sh/chart label for standalone detection" {
    run grep -q 'helm.sh/chart' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm checks app.kubernetes.io/name label for standalone detection" {
    run grep -q 'app.kubernetes.io/name' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

@test "gather_helm checks container images for standalone detection" {
    run grep -q 'spec.template.spec.containers' "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
}

# ============================================================================
# must-gather exclusion tests
# ============================================================================

@test "gather_helm excludes must-gather from standalone deployments jq filter" {
    run grep -A2 'standalone_deployments=.*jq' "${SCRIPTS_DIR}/gather_helm"
    # Verify the jq filter block for deployments contains must-gather exclusion
    run bash -c "sed -n '/^standalone_deployments=/,/^[^ ]/p' '${SCRIPTS_DIR}/gather_helm' | grep -q 'test(\"must-gather\"'"
    [ "$status" -eq 0 ]
}

@test "gather_helm excludes must-gather from standalone statefulsets jq filter" {
    run bash -c "sed -n '/^standalone_statefulsets=/,/^[^ ]/p' '${SCRIPTS_DIR}/gather_helm' | grep -q 'test(\"must-gather\"'"
    [ "$status" -eq 0 ]
}

@test "gather_helm has must-gather exclusion in all three detection paths" {
    # Phase 1 text: grep -v 'must-gather'
    run grep -c "must-gather" "${SCRIPTS_DIR}/gather_helm"
    [ "$status" -eq 0 ]
    # There should be at least 4 occurrences:
    #   1. grep -v 'must-gather' (text output)
    #   2. test("must-gather" (JSON output)
    #   3. test("must-gather" (standalone deployments)
    #   4. test("must-gather" (standalone statefulsets)
    [ "${output}" -ge 4 ]
}

# ============================================================================
# jq filter must-gather exclusion functional tests
# ============================================================================

@test "jq filter excludes chart with must-gather in the name (native releases)" {
    input='[
        {"name":"my-rhdh","namespace":"ns1","chart":"rhdh-hub-1.0.0"},
        {"name":"rhdh-must-gather","namespace":"ns2","chart":"rhdh-must-gather-0.1.0"},
        {"name":"backstage-app","namespace":"ns3","chart":"backstage-1.2.0"}
    ]'
    result=$(echo "$input" | jq -r '.[] | select(.chart | test("backstage|rhdh|developer-hub"; "i")) | select(.chart | test("must-gather"; "i") | not) | .name')
    [[ "$result" =~ "my-rhdh" ]]
    [[ "$result" =~ "backstage-app" ]]
    [[ ! "$result" =~ "rhdh-must-gather" ]]
}

@test "jq filter excludes deployment with must-gather in the name (standalone)" {
    input='{
        "items": [
            {"metadata":{"name":"developer-hub","namespace":"ns1","labels":{"helm.sh/chart":"rhdh-hub-1.0.0"}},"spec":{"template":{"spec":{"containers":[{"image":"quay.io/rhdh/rhdh:latest"}]}}}},
            {"metadata":{"name":"rhdh-must-gather","namespace":"ns2","labels":{"helm.sh/chart":"rhdh-must-gather-0.1.0"}},"spec":{"template":{"spec":{"containers":[{"image":"quay.io/rhdh/must-gather:latest"}]}}}},
            {"metadata":{"name":"backstage","namespace":"ns3","labels":{"helm.sh/chart":"backstage-1.2.0"}},"spec":{"template":{"spec":{"containers":[{"image":"ghcr.io/backstage/backstage:latest"}]}}}}
        ]
    }'
    pattern="backstage|rhdh|developer-hub"
    img_pattern="quay.io/rhdh|registry.redhat.io/rhdh|ghcr.io/backstage/backstage"
    result=$(echo "$input" | jq -r --arg pattern "$pattern" --arg img_pattern "$img_pattern" '
      .items[] |
      select(
        (.metadata.labels["helm.sh/chart"] // "" | test($pattern; "i")) or
        (.metadata.labels["app.kubernetes.io/name"] // "" | test($pattern; "i")) or
        (.metadata.labels["app.kubernetes.io/instance"] // "" | test($pattern; "i")) or
        (.spec.template.spec.containers[]?.image // "" | test($img_pattern; "i")) or
        (.spec.template.spec.initContainers[]?.image // "" | test($img_pattern; "i"))
      ) |
      select(.metadata.name | test("must-gather"; "i") | not) |
      "\(.metadata.namespace)/\(.metadata.name)"
    ')
    [[ "$result" =~ "ns1/developer-hub" ]]
    [[ "$result" =~ "ns3/backstage" ]]
    [[ ! "$result" =~ "must-gather" ]]
}

@test "jq filter is case-insensitive for must-gather exclusion" {
    input='[
        {"name":"my-rhdh","namespace":"ns1","chart":"rhdh-hub-1.0.0"},
        {"name":"RHDH-Must-Gather","namespace":"ns2","chart":"RHDH-Must-Gather-0.1.0"}
    ]'
    result=$(echo "$input" | jq -r '.[] | select(.chart | test("backstage|rhdh|developer-hub"; "i")) | select(.chart | test("must-gather"; "i") | not) | .name')
    [[ "$result" =~ "my-rhdh" ]]
    [[ ! "$result" =~ "Must-Gather" ]]
}
