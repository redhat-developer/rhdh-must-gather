#!/usr/bin/env bats
# Tests for gather_network-policies script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"

    export MOCK_BIN="${TEST_TMPDIR}/mock-bin"
    mkdir -p "$MOCK_BIN"

    cat > "$MOCK_BIN/kubectl" << 'MOCK_KUBECTL'
#!/usr/bin/env bash
if [[ "${1:-}" == "version" ]]; then
    echo "Client Version: v1.30.0"
    exit 0
fi
for arg in "$@"; do
    case "$arg" in
        json) echo '{"items":[]}'; exit 0 ;;
        jsonpath=*) exit 0 ;;
        yaml)
            echo "apiVersion: v1"
            echo "kind: List"
            echo "items: []"
            exit 0
            ;;
    esac
done
echo "NAME"
exit 0
MOCK_KUBECTL
    chmod +x "$MOCK_BIN/kubectl"

    cat > "$MOCK_BIN/oc" << 'MOCK_OC'
#!/usr/bin/env bash
echo ""
exit 0
MOCK_OC
    chmod +x "$MOCK_BIN/oc"

    cat > "$MOCK_BIN/helm" << 'MOCK_HELM'
#!/usr/bin/env bash
echo "[]"
exit 0
MOCK_HELM
    chmod +x "$MOCK_BIN/helm"

    if command -v jq &>/dev/null; then
        ln -sf "$(command -v jq)" "$MOCK_BIN/jq"
    else
        cat > "$MOCK_BIN/jq" << 'MOCK_JQ'
#!/usr/bin/env bash
echo ""
exit 0
MOCK_JQ
        chmod +x "$MOCK_BIN/jq"
    fi

    export PATH="$MOCK_BIN:$PATH"
    export KUBECTL_CMD="kubectl"
}

teardown() {
    teardown_test_environment
}

@test "gather_network-policies script exists" {
    [ -f "${SCRIPTS_DIR}/gather_network-policies" ]
}

@test "gather_network-policies script is executable" {
    [ -x "${SCRIPTS_DIR}/gather_network-policies" ]
}

@test "gather_network-policies sources common.sh" {
    run grep -q "source.*common.sh" "${SCRIPTS_DIR}/gather_network-policies"
    [ "$status" -eq 0 ]
}

@test "must_gather includes network-policies by default" {
    run grep -q 'network-policies' "${SCRIPTS_DIR}/must_gather"
    [ "$status" -eq 0 ]
}

@test "must_gather --help mentions network-policies" {
    run "${SCRIPTS_DIR}/must_gather" --help
    [ "$status" -eq 0 ]
    [[ "$output" =~ "network-policies" ]]
}

@test "targeted namespaces are written and orchestrator file is ignored" {
    export RHDH_TARGET_NAMESPACES="rhdh-prod, rhdh-staging"
    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    echo "knative-serving" > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_network-policies"
    [ "$status" -eq 0 ]

    [ -f "$BASE_COLLECTION_PATH/network-policies/detected-namespaces.txt" ]
    run cat "$BASE_COLLECTION_PATH/network-policies/detected-namespaces.txt"
    [[ "$output" == *"rhdh-prod"* ]]
    [[ "$output" == *"rhdh-staging"* ]]
    [[ "$output" != *"knative-serving"* ]]
}

@test "targeted namespaces collect policy, label, and summary files" {
    export RHDH_TARGET_NAMESPACES="rhdh-prod"

    run "${SCRIPTS_DIR}/gather_network-policies"
    [ "$status" -eq 0 ]

    [ -f "$BASE_COLLECTION_PATH/network-policies/ns=rhdh-prod/networkpolicies.yaml" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/ns=rhdh-prod/networkpolicies.json" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/ns=rhdh-prod/pods-show-labels.txt" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/ns=rhdh-prod/namespace.yaml" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/ns=rhdh-prod/services.yaml" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/peer-namespaces/openshift-monitoring.yaml" ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/summary.txt" ]

    run cat "$BASE_COLLECTION_PATH/network-policies/summary.txt"
    [[ "$output" == *"rhdh-prod"* ]]
    [[ "$output" == *"How to troubleshoot"* ]]
}

@test "auto-detect with nothing found writes no-namespaces.txt and exits 0" {
    unset RHDH_TARGET_NAMESPACES

    run "${SCRIPTS_DIR}/gather_network-policies"
    [ "$status" -eq 0 ]
    [ -f "$BASE_COLLECTION_PATH/network-policies/no-namespaces.txt" ]
    [ ! -f "$BASE_COLLECTION_PATH/network-policies/summary.txt" ]
}

@test "auto-detect merges orchestrator namespaces" {
    unset RHDH_TARGET_NAMESPACES
    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    printf '%s\n' 'knative-serving' '  knative-serving' > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_network-policies"
    [ "$status" -eq 0 ]

    run cat "$BASE_COLLECTION_PATH/network-policies/detected-namespaces.txt"
    [[ "$output" == *"knative-serving"* ]]
    count=$(grep -c '^knative-serving$' "$BASE_COLLECTION_PATH/network-policies/detected-namespaces.txt" || true)
    [ "$count" -eq 1 ]
}