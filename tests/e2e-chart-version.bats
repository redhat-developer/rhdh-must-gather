#!/usr/bin/env bats
# Unit tests for E2E Helm chart version resolution helpers

load 'test_helper'

setup() {
    setup_test_environment
    # shellcheck source=tests/e2e/lib/test-utils.sh
    source "${PROJECT_ROOT}/tests/e2e/lib/test-utils.sh"
}

teardown() {
    teardown_test_environment
}

@test "select_latest_ci_chart_version_from_tags uses version sort for build numbers" {
    run bash -c "source '${PROJECT_ROOT}/tests/e2e/lib/test-utils.sh' && \
        printf '%s\n' 2.0-11-CI 2.0-100-CI 2.0-59-CI | select_latest_ci_chart_version_from_tags 2.0"
    [ "$status" -eq 0 ]
    [ "$output" = "2.0-100-CI" ]
}

@test "select_latest_ci_chart_version_from_tags ignores other major versions" {
    run bash -c "source '${PROJECT_ROOT}/tests/e2e/lib/test-utils.sh' && \
        printf '%s\n' 1.11-60-CI 2.0-11-CI 2.0-59-CI | select_latest_ci_chart_version_from_tags 2.0"
    [ "$status" -eq 0 ]
    [ "$output" = "2.0-59-CI" ]
}

@test "chart_major_version_for_target_branch extracts release branch version" {
    run chart_major_version_for_target_branch "release-2.0"
    [ "$status" -eq 0 ]
    [ "$output" = "2.0" ]
}
