#!/usr/bin/env bats
# Tests for gather_namespace-inspect script

load 'test_helper'

setup() {
    setup_test_environment
    export BASE_COLLECTION_PATH="${TEST_TMPDIR}"

    # Create a mock bin directory and prepend it to PATH
    export MOCK_BIN="${TEST_TMPDIR}/mock-bin"
    mkdir -p "$MOCK_BIN"

    # File where mock oc captures inspect arguments
    export OC_INSPECT_ARGS="${TEST_TMPDIR}/oc-inspect-args.txt"

    # Mock oc: captures "adm inspect" args, no-ops everything else
    cat > "$MOCK_BIN/oc" << 'MOCK_OC'
#!/usr/bin/env bash
if [[ "${1:-}" == "adm" && "${2:-}" == "inspect" ]]; then
    shift 2
    echo "$@" > "$OC_INSPECT_ARGS"
    exit 0
fi
if [[ "${1:-}" == "version" ]]; then
    echo "Client Version: v4.15.0"
    exit 0
fi
echo ""
exit 0
MOCK_OC
    chmod +x "$MOCK_BIN/oc"

    # Mock kubectl: returns empty results for all queries
    # Distinguishes "-o json" (needs valid JSON for jq) from "-o jsonpath=..." (needs empty string)
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
    esac
done
exit 0
MOCK_KUBECTL
    chmod +x "$MOCK_BIN/kubectl"

    # Mock helm: returns empty JSON array
    cat > "$MOCK_BIN/helm" << 'MOCK_HELM'
#!/usr/bin/env bash
echo "[]"
exit 0
MOCK_HELM
    chmod +x "$MOCK_BIN/helm"

    # Mock jq: use real jq if available, otherwise provide minimal stub
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
# Orchestrator namespace integration tests (runtime behavior)
# ============================================================================

@test "gather_namespace-inspect auto-detect deduplicates orchestrator namespaces with whitespace variants" {
    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    # Write duplicates, whitespace variants, and blank lines
    cat > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt" << 'EOF'
orch-ns1
  orch-ns1
orch-ns2

  orch-ns2
orch-ns1
EOF

    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]

    # oc adm inspect should have been called
    [ -f "$OC_INSPECT_ARGS" ]

    # Each namespace should appear exactly once
    local args
    args=$(cat "$OC_INSPECT_ARGS")
    local count_ns1 count_ns2
    count_ns1=$(echo "$args" | grep -o 'namespace/orch-ns1' | wc -l)
    count_ns2=$(echo "$args" | grep -o 'namespace/orch-ns2' | wc -l)
    [ "$count_ns1" -eq 1 ]
    [ "$count_ns2" -eq 1 ]
}

@test "gather_namespace-inspect explicit namespaces mode ignores orchestrator namespaces" {
    export RHDH_TARGET_NAMESPACES="explicit-ns1,explicit-ns2"

    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    echo "orch-ns1" > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
    [ -f "$OC_INSPECT_ARGS" ]

    local args
    args=$(cat "$OC_INSPECT_ARGS")
    [[ "$args" == *"namespace/explicit-ns1"* ]]
    [[ "$args" == *"namespace/explicit-ns2"* ]]
    [[ "$args" != *"namespace/orch-ns1"* ]]
}

@test "gather_namespace-inspect empty detected-namespaces.txt does not add namespaces" {
    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    : > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]

    # With no helm/operator/CR results and an empty orch file, no namespaces are detected
    [ -f "$BASE_COLLECTION_PATH/namespace-inspect/no-namespaces.txt" ]
    [[ ! -f "$OC_INSPECT_ARGS" ]]
}

@test "gather_namespace-inspect absent detected-namespaces.txt does not add namespaces" {
    # Don't create the orchestrator directory at all
    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]

    [ -f "$BASE_COLLECTION_PATH/namespace-inspect/no-namespaces.txt" ]
    [[ ! -f "$OC_INSPECT_ARGS" ]]
}

@test "gather_namespace-inspect merges auto-detected and orchestrator namespaces without duplicates" {
    # Make kubectl return a namespace for the operator detection query
    cat > "$MOCK_BIN/kubectl" << 'MOCK_KUBECTL'
#!/usr/bin/env bash
if [[ "${1:-}" == "version" ]]; then
    echo "Client Version: v1.30.0"
    exit 0
fi
if [[ "$*" == *"app=rhdh-operator"* ]]; then
    echo "operator-ns"
    exit 0
fi
for arg in "$@"; do
    case "$arg" in
        json) echo '{"items":[]}'; exit 0 ;;
        jsonpath=*) exit 0 ;;
    esac
done
exit 0
MOCK_KUBECTL
    chmod +x "$MOCK_BIN/kubectl"

    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    # Include operator-ns as a duplicate and a new orch-only namespace
    printf '%s\n' "operator-ns" "orch-ns1" > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]
    [ -f "$OC_INSPECT_ARGS" ]

    local args
    args=$(cat "$OC_INSPECT_ARGS")
    local count_operator count_orch
    count_operator=$(echo "$args" | grep -o 'namespace/operator-ns' | wc -l)
    count_orch=$(echo "$args" | grep -o 'namespace/orch-ns1' | wc -l)
    [ "$count_operator" -eq 1 ]
    [ "$count_orch" -eq 1 ]
}

@test "gather_namespace-inspect whitespace-only lines in detected-namespaces.txt are ignored" {
    mkdir -p "$BASE_COLLECTION_PATH/orchestrator"
    # File with only whitespace lines
    printf '   \n\t\n  \n' > "$BASE_COLLECTION_PATH/orchestrator/detected-namespaces.txt"

    run "${SCRIPTS_DIR}/gather_namespace-inspect"
    [ "$status" -eq 0 ]

    # No valid namespaces, so no inspect should run
    [ -f "$BASE_COLLECTION_PATH/namespace-inspect/no-namespaces.txt" ]
    [[ ! -f "$OC_INSPECT_ARGS" ]]
}
