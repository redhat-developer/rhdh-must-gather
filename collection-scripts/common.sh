#!/usr/bin/env bash

set -euo pipefail

export LOG_LEVEL="${LOG_LEVEL:-info}"
if [[ "$LOG_LEVEL" == "trace" ]]; then
  set -x
fi

# Only log ERR trap in debug mode to avoid confusing users with expected failures
# (e.g., resource not found errors). Script-level failures are handled by must_gather.
trap 'if [[ "$LOG_LEVEL" == "debug" || "$LOG_LEVEL" == "trace" ]]; then log "DEBUG" "Command failed at line $LINENO (this may be expected)"; fi' ERR

export BASE_COLLECTION_PATH="${BASE_COLLECTION_PATH:-/must-gather}"
mkdir -p "${BASE_COLLECTION_PATH}"

export PROS=${PROS:-5}

# Command timeout (seconds) for kubectl/helm calls
CMD_TIMEOUT="${CMD_TIMEOUT:-90}"

# Color codes for output
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export NC='\033[0m' # No Color

# Logging functions
log() {
    local level="$1"
    shift
    echo -e "[$(date '+%Y-%m-%d %H:%M:%S')] [${level}] $*" >&2
}

log_info() {
    log "INFO" "$@"
}

log_warn() {
    log "WARN" "${YELLOW}$*${NC}"
}

log_error() {
    log "ERROR" "${RED}$*${NC}"
}

log_success() {
    log "SUCCESS" "${GREEN}$*${NC}"
}

log_debug() {
    if [[ "$LOG_LEVEL" == "debug" || "$LOG_LEVEL" == "trace" ]]; then
        log "DEBUG" "${BLUE}$*${NC}"
    fi
}

# Check if a namespace should be included in collection
# Returns 0 (true) if namespace should be included, 1 (false) if it should be skipped
should_include_namespace() {
    local namespace="$1"
    
    # If no namespace filtering is specified, include all namespaces
    if [[ -z "${RHDH_TARGET_NAMESPACES:-}" ]]; then
        return 0
    fi
    
    # Convert comma-separated list to array and check if namespace is included
    IFS=',' read -ra target_ns_array <<< "$RHDH_TARGET_NAMESPACES"
    for target_ns in "${target_ns_array[@]}"; do
        # Trim whitespace
        target_ns=$(echo "$target_ns" | xargs)
        if [[ "$namespace" == "$target_ns" ]]; then
            return 0
        fi
    done
    
    return 1
}

# Get namespace arguments for kubectl/helm commands
# Returns either "-A" for all namespaces or "-n namespace1 -n namespace2..." for specific namespaces
get_namespace_args() {
    if [[ -z "${RHDH_TARGET_NAMESPACES:-}" ]]; then
        echo "--all-namespaces"
    else
        local args=""
        IFS=',' read -ra target_ns_array <<< "$RHDH_TARGET_NAMESPACES"
        for target_ns in "${target_ns_array[@]}"; do
            target_ns=$(echo "$target_ns" | xargs)
            args="$args -n $target_ns"
        done
        echo "$args"
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Determine which kubectl-compatible CLI to use (prefer kubectl, fallback to oc)
# This should be used for all standard Kubernetes operations
get_kubectl_cmd() {
    if command_exists "kubectl"; then
        echo "kubectl"
    elif command_exists "oc"; then
        echo "oc"
    else
        echo ""
    fi
}

# Export KUBECTL_CMD for use in all scripts
# This is set once at script load time for consistency
export KUBECTL_CMD="${KUBECTL_CMD:-$(get_kubectl_cmd)}"

function run() {
  timeout "${CMD_TIMEOUT}" "$@" 2>&1 || true
}

# Check if we have cluster connectivity
check_cluster_connectivity() {
    log_debug "Checking cluster connectivity..."

    if [[ -z "$KUBECTL_CMD" ]]; then
        log_error "No kubectl or oc command available"
        return 1
    fi

    # if ! $KUBECTL_CMD get pods --no-headers --limit=1 >/dev/null 2>&1; then
	  if ! $KUBECTL_CMD version; then
        log_error "Unable to connect to Kubernetes cluster"
        return 1
    fi

    log_debug "Cluster connectivity verified"
    return 0
}

# Validate required environment
validate_environment() {
    local errors=0

    # Check required commands
    if ! (command_exists "kubectl" || command_exists "oc"); then
        log_error "Required 'kubectl' or 'oc' command not found"
        ((errors++))
    fi
    if ! command_exists "helm"; then
        log_error "Required command not found: helm"
        ((errors++))
    fi
    if ! command_exists "jq"; then
        log_error "Required command not found: jq"
        ((errors++))
    fi

    # Check output directory is writable
    if ! touch "$BASE_COLLECTION_PATH/.test" 2>/dev/null; then
        log_error "Output directory is not writable: $BASE_COLLECTION_PATH"
        ((errors++))
    else
        rm -f "$BASE_COLLECTION_PATH/.test"
    fi

    # Check cluster connectivity
    if ! check_cluster_connectivity; then
        ((errors++))
    fi

    return $errors
}

# Create directory if it doesn't exist
ensure_directory() {
    local dir="$1"
    if [[ ! -d "$dir" ]]; then
        log_debug "Creating directory: $dir"
        mkdir -p "$dir"
    fi
}

# Check if running in container
is_container() {
    [[ -f /.dockerenv ]] || [[ -n "${KUBERNETES_SERVICE_HOST:-}" ]]
}

# Initialize must-gather environment
init_must_gather() {
    log_info "Initializing must-gather environment"
    log_debug "Must-gather directory: $BASE_COLLECTION_PATH"
    log_debug "Log level: $LOG_LEVEL"
    log_debug "Collection timeout: ${CMD_TIMEOUT}s"
    log_debug "Container environment: $(is_container && echo "yes" || echo "no")"

    # Validate environment
    if ! validate_environment; then
        log_error "Environment validation failed"
        return 1
    fi

    # Create base directories
    ensure_directory "$BASE_COLLECTION_PATH"

    log_info "Must-gather environment initialized"
    return 0
}

# Safe command execution with timeout
safe_exec() {
    local cmd="$1 || true"
    local output_file="$2"
    local description="${3:-}"

    if [[ -n "$description" ]]; then
        log_info "\tCollecting: $description"
    fi

    log_debug "\tExecuting: $cmd"
    log_debug "\tOutput file: $output_file"

    # Ensure output directory exists
    mkdir -p "$(dirname "$output_file")"

    local ret
    # Run in background so we can poll for RHDH_INTERRUPTED; bash defers the INT trap until the
    # current foreground command completes, so a short sleep in the loop lets the trap run on Ctrl-C.
    timeout "$CMD_TIMEOUT" bash -c "$cmd" > "$output_file" 2>&1 &
    local pid=$!
    while kill -0 "$pid" 2>/dev/null; do
        if [[ -n "${RHDH_INTERRUPTED:-}" ]]; then
            kill -INT "$pid" 2>/dev/null
            # Disable errexit so wait (non-zero if child exited by signal) doesn't abort before we exit 130.
            set +e
            wait "$pid" 2>/dev/null
            set -e
            log_warn "Collection interrupted by user (Ctrl-C). Sanitizing collected data..."
            exit 130
        fi
        # || true so an interrupted sleep (e.g. SIGINT) doesn't trigger set -e and abort the script.
        sleep 0.5 || true
    done
    # Disable errexit so we capture wait's status (timeout/failure/signal) and reach the failure-handling
    # block below instead of exiting; safe_exec is meant to be "safe" and not abort the run on timeouts.
    set +e
    wait "$pid" 2>/dev/null
    ret=$?
    set -e
    if [[ -n "${RHDH_INTERRUPTED:-}" ]]; then
        log_warn "Collection interrupted. Sanitizing collected data..."
        exit 130
    fi
    if [[ $ret -ne 0 ]]; then
        # Propagate SIGINT/SIGTERM so the main script exits and runs the EXIT (sanitize) trap.
        if [[ $ret -eq 130 || $ret -eq 143 ]]; then
            exit $ret
        fi
        local exec_err
        exec_err=$(cat "$output_file" 2>/dev/null)
        log_warn "\tCommand timed out or failed: $cmd${exec_err:+ — $exec_err}"
        {
            echo "Command failed or timed out: $cmd"
            echo "Timestamp: $(date)"
            echo "Timeout: ${CMD_TIMEOUT}s"
            echo ""
            echo "=== Error Details ==="
            echo "${exec_err:-No error output captured}"
        } > "$output_file"
    fi
}

collect_rhdh_info_from_running_pods() {
  local ns="$1"
  local labels="$2"
  local output_dir="$3"
  local owner_kind="${4:-}"  # Optional: "deployment" or "statefulset" to filter by owner

  # Get all running pods matching the labels, optionally filtered by owner kind.
  # Deployment pods are owned by ReplicaSets; StatefulSet pods are owned directly.
  local running_pods
  local _owner_ref_kind=""
  if [[ "$owner_kind" == "deployment" ]]; then
    _owner_ref_kind="ReplicaSet"
  elif [[ "$owner_kind" == "statefulset" ]]; then
    _owner_ref_kind="StatefulSet"
  fi

  if [[ -n "$_owner_ref_kind" ]]; then
    running_pods=$(
      $KUBECTL_CMD get pods -n "$ns" -l "$labels" -o json 2>/dev/null \
        | jq -r --arg ok "$_owner_ref_kind" \
          '.items[] | select(.status.phase == "Running") | select(any(.metadata.ownerReferences[]?; .kind == $ok)) | .metadata.name' || true
    )
  else
    running_pods=$(
      $KUBECTL_CMD get pods -n "$ns" \
        -l "$labels" \
        -o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}'
    )
  fi

  if [ -z "$running_pods" ]; then
    log_warn "No running pod found in $ns namespace with labels: $labels => no data will be fetched from the running app"
    return 0
  fi

  # Use the first running pod for application-level metadata (identical across replicas)
  local first_pod
  first_pod=$(echo "$running_pods" | head -n1)

  # Running user ID
  safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- id 2>/dev/null" "$output_dir/app-container-userid.txt" "id inside the main container"

  # Collect relevant environment variables from the container
  log_info "\tCollecting: environment variables from container"
  local env_vars_file="$output_dir/env-vars.txt"
  if ! $KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c '
    echo "=== RHDH/Backstage Environment Variables ==="
    echo ""
    env | grep -E "^(BACKSTAGE_|RHDH_|UPSTREAM_REPO|MIDSTREAM_REPO|NODE_|APP_CONFIG_|LOG_LEVEL|PLUGIN_|NO_PROXY|HTTP_PROXY|HTTPS_PROXY|NPM_CONFIG_|GLOBAL_AGENT_)" | sort || true
  ' > "$env_vars_file" 2>&1; then
    local exec_err
    exec_err=$(cat "$env_vars_file" 2>/dev/null)
    log_warn "Failed to collect environment variables from container: ${exec_err:-unknown error}"
  fi

  # Extract specific env vars for version/metadata collection
  local backstage_version=""
  local rhdh_version=""
  local upstream_repo=""
  local midstream_repo=""

  local _env_output
  # shellcheck disable=SC2016 # Variables are intentionally expanded inside the container, not on the host
  if _env_output=$($KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c '
    echo "BACKSTAGE_VERSION=${BACKSTAGE_VERSION:-}"
    echo "RHDH_VERSION=${RHDH_VERSION:-}"
    echo "UPSTREAM_REPO=${UPSTREAM_REPO:-}"
    echo "MIDSTREAM_REPO=${MIDSTREAM_REPO:-}"
  ' 2>&1); then
    backstage_version=$(echo "$_env_output" | sed -n 's/^BACKSTAGE_VERSION=//p')
    rhdh_version=$(echo "$_env_output" | sed -n 's/^RHDH_VERSION=//p')
    upstream_repo=$(echo "$_env_output" | sed -n 's/^UPSTREAM_REPO=//p')
    midstream_repo=$(echo "$_env_output" | sed -n 's/^MIDSTREAM_REPO=//p')
  else
    log_warn "Failed to extract version env vars from pod $first_pod: ${_env_output:-unknown error}"
  fi

  # Build Metadata to extract the RHDH version information
  # Primary: Use BACKSTAGE_VERSION env var; Fallback: Read backstage.json file
  if [[ -n "$backstage_version" ]]; then
    log_info "\tCollecting: backstage version from BACKSTAGE_VERSION env var"
    echo "{\"version\": \"$backstage_version\", \"source\": \"BACKSTAGE_VERSION env var\"}" | jq '.' > "$output_dir/backstage.json"
  else
    log_debug "BACKSTAGE_VERSION env var not set, falling back to backstage.json file"
    safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- cat /opt/app-root/src/backstage.json 2>/dev/null" "$output_dir/backstage.json" "backstage.json (fallback)"
  fi

  # Primary: Use RHDH_VERSION, UPSTREAM_REPO, MIDSTREAM_REPO env vars; Fallback: Read build-metadata.json file
  if [[ -n "$rhdh_version" || -n "$upstream_repo" || -n "$midstream_repo" ]]; then
    log_info "\tCollecting: build metadata from environment variables"
    jq -n \
      --arg rhdh_version "$rhdh_version" \
      --arg upstream_repo "$upstream_repo" \
      --arg midstream_repo "$midstream_repo" \
      '{
        rhdh_version: $rhdh_version,
        upstream_repo: $upstream_repo,
        midstream_repo: $midstream_repo,
        source: "environment variables"
      }' > "$output_dir/build-metadata.json"
  else
    log_debug "Build metadata env vars not set, falling back to build-metadata.json file"
    safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- cat /opt/app-root/src/packages/app/src/build-metadata.json 2>/dev/null | jq '.card'" "$output_dir/build-metadata.json" "build metadata (fallback)"
  fi

  # Node version
  safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- node --version 2>/dev/null" "$output_dir/node-version.txt" "Node version"

  # dynamic-plugins-root on the filesystem
  safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- ls -lhrta dynamic-plugins-root 2>/dev/null" "$output_dir/dynamic-plugins-root.fs.txt" "dynamic-plugins-root dir on the filesystem"

  # app-config generated by the dynamic plugins installer (init container)
  safe_exec "$KUBECTL_CMD -n '$ns' exec '$first_pod' -- cat /opt/app-root/src/dynamic-plugins-root/app-config.dynamic-plugins.yaml 2>/dev/null" "$output_dir/app-config.dynamic-plugins.yaml" "app-config.dynamic-plugins.yaml file"

  # Collect all running processes from all containers in ALL running pods
  # Use || true to ensure process collection failure doesn't stop the rest of data collection
  while IFS= read -r pod; do
    [ -z "$pod" ] && continue
    collect_container_processes "$ns" "$pod" "$output_dir" || true
  done <<< "$running_pods"
}

# Collect all running processes from containers in a pod using /proc filesystem
# This is useful for correlating with memory dumps to identify orphaned processes
# Note: Uses /proc directly since 'ps' is not available in the container image
collect_container_processes() {
  local ns="$1"
  local pod="$2"
  local output_dir="$3"

  log_info "Collecting process list from containers in pod: $pod"

  # Create per-pod directory to support multiple replicas
  local processes_dir="$output_dir/processes/pod=${pod}"
  ensure_directory "$processes_dir"

  # Get all containers in the pod
  local containers
  containers=$($KUBECTL_CMD get pod -n "$ns" "$pod" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || true)

  if [[ -z "$containers" ]]; then
    log_warn "No containers found in pod $pod"
    echo "No containers found" > "$processes_dir/no-containers.txt"
    return 0
  fi

  for container in $containers; do
    log_debug "Collecting processes from container: $container"

    local container_file="$processes_dir/container=${container}.txt"

    # Collect all processes using /proc filesystem (ps is not available)
    # Captures: PID, PPID, State, RSS memory, Virtual memory, Process name, Command line
    # Note: Variables are passed as positional arguments to avoid shell quoting issues
    # shellcheck disable=SC2016 # Vars in single quotes are intentional; expanded by inner sh, not outer shell
    if ! $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c '
      # Receive variables as positional arguments
      _container="$1"
      _pod="$2"
      _ns="$3"

      # Save our own PID to exclude it from the list (this is the collection script itself)
      my_pid=$$

      echo "=== Process List (from /proc filesystem) ==="
      echo "Container: $_container"
      echo "Pod: $_pod"
      echo "Namespace: $_ns"
      echo "Collected at: $(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"
      echo ""
      printf "%-7s %-7s %-5s %-10s %-10s %-20s %s\n" "PID" "PPID" "STATE" "RSS(KB)" "VSZ(KB)" "NAME" "CMDLINE"
      printf "%-7s %-7s %-5s %-10s %-10s %-20s %s\n" "-------" "-------" "-----" "----------" "----------" "--------------------" "-------"

      proc_count=0
      for pid_dir in /proc/[0-9]*; do
        pid=$(basename "$pid_dir")

        # Skip our own process (the collection script shell)
        if [ "$pid" = "$my_pid" ]; then
          continue
        fi

        # Verify this is a valid process directory
        if [ ! -d "$pid_dir" ] || [ ! -f "$pid_dir/status" ]; then
          continue
        fi

        # Get process name from comm (more reliable than parsing cmdline)
        name=""
        if [ -f "$pid_dir/comm" ]; then
          name=$(cat "$pid_dir/comm" 2>/dev/null || echo "")
        fi

        # Parse status file for PPID, State, and memory info
        ppid=""
        state=""
        rss=""
        vsz=""
        if [ -f "$pid_dir/status" ]; then
          while IFS= read -r line; do
            case "$line" in
              PPid:*)  ppid=$(echo "$line" | awk "{print \$2}") ;;
              State:*) state=$(echo "$line" | awk "{print \$2}") ;;
              VmRSS:*) rss=$(echo "$line" | awk "{print \$2}") ;;
              VmSize:*) vsz=$(echo "$line" | awk "{print \$2}") ;;
            esac
          done < "$pid_dir/status"
        fi

        # Get command line (null-separated, convert to spaces)
        cmdline=""
        if [ -f "$pid_dir/cmdline" ]; then
          cmdline=$(cat "$pid_dir/cmdline" 2>/dev/null | tr "\0" " " | head -c 200 || echo "")
          # Trim trailing space
          cmdline=$(echo "$cmdline" | sed "s/ *$//")
        fi

        # If cmdline is empty, try to get from comm or show as kernel thread
        if [ -z "$cmdline" ] && [ -n "$name" ]; then
          cmdline="[$name]"
        fi

        # Default values for missing fields
        rss=${rss:-"-"}
        vsz=${vsz:-"-"}

        printf "%-7s %-7s %-5s %-10s %-10s %-20s %s\n" "$pid" "$ppid" "$state" "$rss" "$vsz" "$name" "$cmdline"
        proc_count=$((proc_count + 1))
      done

      echo ""
      echo "=== Process Count ==="
      echo "Total processes: $proc_count"

      echo ""
      echo "=== Memory Summary ==="
      if [ -f /proc/meminfo ]; then
        grep -E "^(MemTotal|MemFree|MemAvailable|Buffers|Cached|SwapTotal|SwapFree):" /proc/meminfo
      fi
    ' -- "$container" "$pod" "$ns" > "$container_file" 2>&1; then
      local exec_err
      exec_err=$(cat "$container_file" 2>/dev/null)
      log_warn "Failed to collect processes from container $container: ${exec_err:-unknown error}"
      {
        echo "Failed to collect processes from container $container"
        echo "The container may not be running or may not have /proc mounted"
        echo ""
        echo "=== Error Details ==="
        echo "${exec_err:-No error output captured}"
      } > "$container_file"
    fi
  done

  log_debug "Process collection completed for pod: $pod"
  return 0
}

# Send a signal to a process in a container
# Tries 'kill' command first, falls back to Node.js if kill is not available
# Arguments: ns, pod, container, pid, signal (e.g., USR1, USR2)
# Returns: 0 on success, 1 on failure
send_signal_to_process() {
  local ns="$1"
  local pod="$2"
  local container="$3"
  local pid="$4"
  local signal="$5"

  # Try kill command first (works in most containers)
  if $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- kill -"$signal" "$pid" 2>/dev/null; then
    return 0
  fi

  # Fall back to Node.js process.kill() if kill command not available
  # This works because we know Node.js is running in the container
  if $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- \
      node -e "process.kill($pid, 'SIG$signal')" 2>/dev/null; then
    return 0
  fi

  return 1
}

# Collect heap dump via Node.js inspector protocol
# This is more reliable than SIGUSR2 because:
# 1. SIGUSR1 can activate inspector even if --inspect wasn't passed at startup
# 2. The inspector protocol provides feedback on success/failure
# 3. Heap dump location is controlled by the script, not by Node.js defaults
#
# Prerequisites:
# - Node.js process must be running
# - For best results, start with NODE_OPTIONS="--inspect=0.0.0.0:9229"
#   (without --inspect, we send SIGUSR1 to activate inspector dynamically)
#
# Returns:
# - 0 if heap dump was successfully collected
# - 1 if collection failed (caller should try fallback method)
collect_heap_dump_via_inspector() {
  local ns="$1"
  local pod="$2"
  local container="$3"
  local node_pid="$4"
  local output_file="$5"
  local log_file="$6"

  local inspector_timeout="${HEAP_DUMP_TIMEOUT:-600}"
  local ws_buffer_size="${HEAP_DUMP_BUFFER_SIZE:-16777216}"  # 16MB default
  local port_forward_pid=""

  # Use temp directory under BASE_COLLECTION_PATH (mounted PVC) instead of /tmp
  # to avoid exceeding ephemeral storage limits (heap dumps can be 100MB+)
  local inspector_temp_dir="${BASE_COLLECTION_PATH:-.}/.inspector_tmp"
  mkdir -p "$inspector_temp_dir" 2>/dev/null || inspector_temp_dir="/tmp"

  # Cleanup function - ensures port-forward and temp files are cleaned up
  # even on unexpected exits (via trap)
  cleanup_inspector() {
    if [[ -n "${port_forward_pid:-}" ]] && kill -0 "$port_forward_pid" 2>/dev/null; then
      kill "$port_forward_pid" 2>/dev/null || true
      wait "$port_forward_pid" 2>/dev/null || true
    fi
    # Clean up all temp files (use default to avoid unbound variable with set -u)
    local tmp_dir="${inspector_temp_dir:-}"
    if [[ -n "$tmp_dir" ]]; then
      rm -f "$tmp_dir/inspector_fifo_$$" "$tmp_dir/inspector_out_$$" "$tmp_dir/heapdump_chunks_$$" \
            "$tmp_dir/inspector_cleaned_$$" "$tmp_dir/inspector_fallback_fifo_$$" \
            "$tmp_dir/inspector_fallback_out_$$" 2>/dev/null || true
      rmdir "$tmp_dir" 2>/dev/null || true
    fi
  }
  # Alias for backward compatibility within this function
  cleanup_port_forward() { cleanup_inspector; }

  # Set trap to ensure cleanup on any exit from this function
  trap cleanup_inspector RETURN

  log_info "Attempting heap dump via inspector protocol for $pod/$container (PID: $node_pid)"

  {
    echo "=== Inspector Protocol Heap Dump Collection ==="
    echo "Timestamp: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo "Pod: $pod"
    echo "Container: $container"
    echo "Node.js PID: $node_pid"
    echo ""
  } >> "$log_file"

  # Detect inspector port from process command line or environment
  # Default is 9229, but user may have configured a different port via --inspect=host:port
  local inspector_port=9229
  local detected_port=""

  # Try to detect port from cmdline (--inspect=0.0.0.0:9230 or --inspect=:9230 or --inspect=9230)
  detected_port=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "
    # Check cmdline for --inspect or --inspect-brk with custom port
    cmdline=\$(cat /proc/$node_pid/cmdline 2>/dev/null | tr '\0' ' ')
    # Match --inspect=host:port, --inspect=:port, or --inspect=port
    echo \"\$cmdline\" | grep -oE '\\-\\-inspect(-brk)?=[^[:space:]]*' | head -1 | grep -oE '[0-9]+\$'
  " 2>/dev/null) || true

  if [[ -n "$detected_port" && "$detected_port" =~ ^[0-9]+$ ]]; then
    inspector_port="$detected_port"
    echo "Detected custom inspector port from cmdline: $inspector_port" >> "$log_file"
  else
    # Try to detect from NODE_OPTIONS environment variable
    detected_port=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "
      env_opts=\$(cat /proc/$node_pid/environ 2>/dev/null | tr '\0' '\n' | grep '^NODE_OPTIONS=' | head -1)
      echo \"\$env_opts\" | grep -oE '\\-\\-inspect(-brk)?=[^[:space:]]*' | head -1 | grep -oE '[0-9]+\$'
    " 2>/dev/null) || true

    if [[ -n "$detected_port" && "$detected_port" =~ ^[0-9]+$ ]]; then
      inspector_port="$detected_port"
      echo "Detected custom inspector port from NODE_OPTIONS: $inspector_port" >> "$log_file"
    else
      echo "Using default inspector port: $inspector_port" >> "$log_file"
    fi
  fi

  local local_port=$((inspector_port + RANDOM % 1000))  # Avoid port conflicts

  # Step 1: Check if inspector is already enabled by looking for the port
  # Convert port to hex for /proc/net/tcp lookup (e.g., 9229 -> 2411)
  local port_hex
  port_hex=$(printf '%04X' "$inspector_port")

  log_debug "Checking if inspector is already active on port $inspector_port (hex: $port_hex)..."
  local inspector_active=false
  if $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "
    # Check if the inspector port is in use by looking at /proc/net/tcp
    # Port is in hex format in the local_address column (second field after colon)
    grep -qi ':$port_hex' /proc/net/tcp 2>/dev/null || \
    grep -qi ':$port_hex' /proc/net/tcp6 2>/dev/null
  " 2>/dev/null; then
    inspector_active=true
    echo "Inspector appears to be already active (port $inspector_port in use)" >> "$log_file"
  fi

  # Step 2: If inspector not active, send SIGUSR1 to activate it
  if [[ "$inspector_active" != "true" ]]; then
    log_info "Sending SIGUSR1 to activate inspector..."
    echo "Sending SIGUSR1 to PID $node_pid to activate inspector..." >> "$log_file"

    if ! send_signal_to_process "$ns" "$pod" "$container" "$node_pid" "USR1" 2>> "$log_file"; then
      echo "Failed to send SIGUSR1 signal (neither kill nor node available)" >> "$log_file"
      log_warn "Failed to send SIGUSR1 to activate inspector"
      return 1
    fi

    # Wait for inspector to start
    echo "Waiting for inspector to start..." >> "$log_file"
    sleep 2
  fi

  # Step 3: Start port-forward in background (with retry logic)
  local port_forward_attempts=0
  local max_port_forward_attempts=2
  local port_forward_success=false

  while [[ $port_forward_attempts -lt $max_port_forward_attempts && "$port_forward_success" != "true" ]]; do
    port_forward_attempts=$((port_forward_attempts + 1))

    # On retry, force SIGUSR1 to reactivate inspector
    if [[ $port_forward_attempts -gt 1 ]]; then
      echo "" >> "$log_file"
      echo "=== Retry attempt $port_forward_attempts ===" >> "$log_file"
      log_info "Retrying with SIGUSR1 to reactivate inspector..."
      send_signal_to_process "$ns" "$pod" "$container" "$node_pid" "USR1" 2>> "$log_file" || true
      sleep 2
      # Get a new local port for retry
      local_port=$((local_port + 1))
    fi

    log_debug "Starting port-forward to $pod:$inspector_port on local port $local_port"
    echo "Starting port-forward: localhost:$local_port -> $pod:$inspector_port" >> "$log_file"

    $KUBECTL_CMD port-forward -n "$ns" "pod/$pod" "$local_port:$inspector_port" >> "$log_file" 2>&1 &
    port_forward_pid=$!

    # Wait for port-forward to be ready
    local wait_count=0
    local port_forward_failed=false

    while ! curl -s "http://localhost:$local_port/json" >/dev/null 2>&1; do
      sleep 0.5
      wait_count=$((wait_count + 1))
      if [[ $wait_count -gt 20 ]]; then
        {
          echo "Timeout waiting for port-forward to be ready (waited 10 seconds)"
          echo ""
          echo "=== Port-forward diagnostics ==="
          echo "Local port: $local_port"
          echo "Target: $pod:$inspector_port"
          echo ""
          echo "Curl error (last attempt):"
          curl -s "http://localhost:$local_port/json" 2>&1 || true
          echo ""
          echo "Possible causes:"
          echo "  - Inspector not listening on port $inspector_port"
          echo "  - Node.js process doesn't support SIGUSR1 inspector activation"
          echo "  - Network policy blocking the connection"
          echo "  - Inspector bound to 127.0.0.1 instead of 0.0.0.0"
        } >> "$log_file"
        port_forward_failed=true
        break
      fi
      # Check if port-forward process is still running
      if ! kill -0 "$port_forward_pid" 2>/dev/null; then
        {
          echo "Port-forward process died unexpectedly"
          echo ""
          echo "=== Port-forward diagnostics ==="
          echo "The kubectl port-forward process terminated before connection was established."
          echo "This usually means the target port ($inspector_port) is not open in the container."
          echo ""
          echo "Possible causes:"
          echo "  - Inspector not enabled (Node.js not started with --inspect)"
          echo "  - SIGUSR1 failed to activate the inspector"
          echo "  - Container security context prevents port binding"
        } >> "$log_file"
        port_forward_failed=true
        break
      fi
    done

    if [[ "$port_forward_failed" == "true" ]]; then
      cleanup_port_forward
      if [[ $port_forward_attempts -lt $max_port_forward_attempts ]]; then
        log_warn "Port-forward failed, will retry with SIGUSR1..."
      fi
    else
      port_forward_success=true
    fi
  done

  if [[ "$port_forward_success" != "true" ]]; then
    log_warn "Port-forward failed after $port_forward_attempts attempts"
    return 1
  fi

  echo "Port-forward established successfully" >> "$log_file"

  # Step 4: Get WebSocket URL from inspector
  log_debug "Fetching inspector WebSocket URL..."
  local ws_url
  ws_url=$(curl -s "http://localhost:$local_port/json" | jq -r '.[0].webSocketDebuggerUrl' 2>/dev/null)

  if [[ -z "$ws_url" || "$ws_url" == "null" ]]; then
    echo "Failed to get WebSocket URL from inspector" >> "$log_file"
    echo "Inspector /json response:" >> "$log_file"
    curl -s "http://localhost:$local_port/json" >> "$log_file" 2>&1 || true
    log_warn "Failed to get inspector WebSocket URL"
    cleanup_port_forward
    return 1
  fi

  # Replace the remote address with localhost since we're using port-forward
  ws_url=$(echo "$ws_url" | sed "s|ws://[^:]*:|ws://localhost:|" | sed "s|:$inspector_port/|:$local_port/|")
  echo "WebSocket URL: $ws_url" >> "$log_file"

  # Step 5: Use HeapProfiler.takeHeapSnapshot via WebSocket
  # This approach provides progress reporting and streams the snapshot data directly.
  log_info "Triggering heap dump via inspector protocol..."
  echo "" >> "$log_file"
  echo "=== Inspector Protocol Communication ===" >> "$log_file"

  # Create temp files for communication (use inspector_temp_dir to avoid ephemeral storage limits)
  local fifo="$inspector_temp_dir/inspector_fifo_$$"
  local outfile="$inspector_temp_dir/inspector_out_$$"
  local heapfile="$inspector_temp_dir/heapdump_chunks_$$"
  rm -f "$fifo" "$outfile" "$heapfile" 2>/dev/null || true

  if ! mkfifo "$fifo" 2>> "$log_file"; then
    echo "Failed to create FIFO: $fifo" >> "$log_file"
    log_warn "Failed to create FIFO for inspector communication"
    return 1
  fi
  if ! touch "$outfile" "$heapfile" 2>> "$log_file"; then
    echo "Failed to create temp files" >> "$log_file"
    log_warn "Failed to create temp files for inspector communication"
    return 1
  fi

  # Start websocat in background
  # Flags:
  #   -B <size>   - buffer size for large heap snapshot chunks (default: 16MB)
  #   -t          - text mode (not binary)
  websocat -t -B "$ws_buffer_size" "$ws_url" < "$fifo" > "$outfile" 2>> "$log_file" &
  local websocat_pid=$!

  # Give websocat a moment to connect
  sleep 0.5

  # Check if websocat is still running (connection succeeded)
  if ! kill -0 "$websocat_pid" 2>/dev/null; then
    echo "Websocat failed to connect to inspector WebSocket" >> "$log_file"
    log_warn "Failed to establish WebSocket connection to inspector"
    cleanup_port_forward
    rm -f "$fifo" "$outfile" "$heapfile"
    return 1
  fi

  # Open fifo for writing
  exec 3>"$fifo"

  # Step 5a: Ping test - verify two-way WebSocket communication
  echo "=== Ping Test ===" >> "$log_file"
  echo "Testing two-way WebSocket communication..." >> "$log_file"
  local ping_command='{"id":0,"method":"Runtime.evaluate","params":{"expression":"JSON.stringify({v:process.version,pid:process.pid,time:Date.now()})","returnByValue":true}}'
  echo "$ping_command" >&3

  # Wait for ping response (max 10 seconds)
  local ping_wait=0
  local ping_received=false
  while [[ "$ping_received" != "true" && $ping_wait -lt 10 ]]; do
    sleep 1
    ping_wait=$((ping_wait + 1))
    if grep -qa '"id":0' "$outfile" 2>/dev/null; then
      ping_received=true
    fi
  done

  if [[ "$ping_received" != "true" ]]; then
    {
      echo "Ping test FAILED - no response received after ${ping_wait}s"
      echo "This indicates WebSocket responses are not coming back."
      echo "Possible causes:"
      echo "  - Network proxy/mesh intercepting WebSocket traffic"
      echo "  - Firewall or network policy blocking responses"
      echo "  - Inspector in unexpected state"
    } >> "$log_file"
    log_warn "WebSocket ping test failed - two-way communication broken"
    exec 3>&-
    kill "$websocat_pid" 2>/dev/null || true
    wait "$websocat_pid" 2>/dev/null || true
    cleanup_port_forward
    rm -f "$fifo" "$outfile" "$heapfile"
    return 1
  fi

  local ping_response
  ping_response=$(grep '"id":0' "$outfile" | head -1)
  echo "Ping response: $ping_response" >> "$log_file"
  log_debug "WebSocket ping test passed - two-way communication confirmed"

  # Clear the output file for heap dump collection
  : > "$outfile"

  # Step 5b: Enable HeapProfiler domain
  {
    echo ""
    echo "=== Heap Dump Collection ==="
    echo "Enabling HeapProfiler domain..."
  } >> "$log_file"
  echo '{"id":1,"method":"HeapProfiler.enable"}' >&3
  sleep 0.5

  # Step 5c: Start heap snapshot with progress reporting
  echo "Starting heap snapshot with progress reporting..." >> "$log_file"
  log_info "Taking heap snapshot (this may take several minutes for large heaps)..."
  echo '{"id":2,"method":"HeapProfiler.takeHeapSnapshot","params":{"reportProgress":true}}' >&3

  # Wait for completion, checking progress periodically
  # Note: We don't process chunks during collection to avoid race conditions.
  # All chunk extraction happens after websocat completes.
  local wait_time=0
  local max_wait=$inspector_timeout
  local snapshot_complete=false
  local last_reported_pct=-1
  local last_size=0
  local last_cpu_time=0
  local stall_time=0
  local cpu_stall_time=0

  # Helper to get CPU time (utime + stime) from /proc/<pid>/stat
  get_cpu_time() {
    $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- \
      sh -c "cat /proc/$node_pid/stat 2>/dev/null | awk '{print \$14+\$15}'" 2>/dev/null || echo "0"
  }

  # Get initial CPU time
  last_cpu_time=$(get_cpu_time)
  echo "Initial CPU time: $last_cpu_time" >> "$log_file"

  while [[ "$snapshot_complete" != "true" && $wait_time -lt $max_wait ]]; do
    sleep 1
    wait_time=$((wait_time + 1))

    # Check if websocat is still running
    if ! kill -0 "$websocat_pid" 2>/dev/null; then
      echo "Websocat process ended after ${wait_time}s" >> "$log_file"
      break
    fi

    # Check for completion or progress (read-only, don't modify the file)
    if grep -qa '"id":2' "$outfile" 2>/dev/null; then
      if grep -q '"result":{}' "$outfile" 2>/dev/null; then
        snapshot_complete=true
        echo "HeapProfiler.takeHeapSnapshot completed successfully" >> "$log_file"
      elif grep -q '"error"' "$outfile" 2>/dev/null; then
        local error_line error_msg
        error_line=$(grep -a '"id":2' "$outfile" | grep -a '"error"' | head -1)
        error_msg=$(echo "$error_line" | jq -r '.error.message // "unknown error"' 2>/dev/null)
        echo "HeapProfiler.takeHeapSnapshot failed: $error_msg" >> "$log_file"
        log_warn "Heap snapshot failed: $error_msg"
        exec 3>&-
        kill "$websocat_pid" 2>/dev/null || true
        wait "$websocat_pid" 2>/dev/null || true
        cleanup_port_forward
        rm -f "$fifo" "$outfile" "$heapfile"
        return 1
      fi
    fi

    # Check for progress events in recent lines (use tail to avoid reading huge file)
    local latest_progress
    latest_progress=$(tail -100 "$outfile" 2>/dev/null | grep -a '"HeapProfiler.reportHeapSnapshotProgress"' | tail -1)
    if [[ -n "$latest_progress" ]]; then
      local done_val total_val pct
      done_val=$(echo "$latest_progress" | jq -r '.params.done // 0' 2>/dev/null)
      total_val=$(echo "$latest_progress" | jq -r '.params.total // 1' 2>/dev/null)
      if [[ "$total_val" -gt 0 ]]; then
        pct=$((done_val * 100 / total_val))
        # Only log every 10% to avoid spam
        if [[ $((pct / 10)) -gt $((last_reported_pct / 10)) ]]; then
          log_info "Heap snapshot progress: ${pct}% (${done_val}/${total_val})"
          echo "Progress: ${pct}% (${done_val}/${total_val})" >> "$log_file"
          last_reported_pct=$pct
        fi
      fi
    fi

    # Monitor for stalled connections (no data flowing)
    local current_size
    current_size=$(stat -c%s "$outfile" 2>/dev/null || echo 0)

    if [[ "$current_size" -eq "$last_size" ]]; then
      stall_time=$((stall_time + 1))

      # Check CPU activity every 10 seconds when data is stalled
      if [[ $((stall_time % 10)) -eq 0 ]]; then
        local current_cpu_time
        current_cpu_time=$(get_cpu_time)

        # Compare against PREVIOUS reading (not initial), detect any change
        if [[ "$current_cpu_time" -ne "$last_cpu_time" ]]; then
          # CPU time changed - process is active
          local cpu_delta=$((current_cpu_time - last_cpu_time))
          echo "CPU active: delta=${cpu_delta} ticks (prev: $last_cpu_time, now: $current_cpu_time) - V8 is working" >> "$log_file"
          log_debug "No data for ${stall_time}s but CPU active (delta=${cpu_delta} ticks) - V8 is working"
          cpu_stall_time=0
        else
          # CPU time unchanged - process may be idle
          cpu_stall_time=$((cpu_stall_time + 10))
          echo "CPU unchanged for ${cpu_stall_time}s (time: $current_cpu_time)" >> "$log_file"
        fi
        # Always update last_cpu_time for next comparison
        last_cpu_time=$current_cpu_time
      fi

      # Log warnings at key intervals but don't abort - wait for full timeout
      if [[ $stall_time -eq 60 ]]; then
        if [[ $cpu_stall_time -ge 60 ]]; then
          log_warn "No data for 60s and CPU unchanged - possible stall (will keep waiting)"
        else
          log_info "No data for 60s but CPU active - V8 is working (will keep waiting)"
        fi
      elif [[ $stall_time -eq 180 ]]; then
        log_warn "No data for 3 minutes - heap serialization may be slow or connection stalled"
      elif [[ $stall_time -eq 300 ]]; then
        log_warn "No data for 5 minutes - consider increasing HEAP_DUMP_TIMEOUT if this completes"
      fi
    else
      # Data is flowing - reset all stall counters
      stall_time=0
      cpu_stall_time=0
      last_size=$current_size
    fi

    # Show periodic status based on file size
    if [[ $((wait_time % 30)) -eq 0 && $wait_time -gt 0 ]]; then
      local human_size
      if [[ $current_size -gt 1048576 ]]; then
        human_size="$((current_size / 1048576))MB"
      elif [[ $current_size -gt 1024 ]]; then
        human_size="$((current_size / 1024))KB"
      else
        human_size="${current_size}B"
      fi
      log_info "Collecting heap data... ${wait_time}s elapsed, ${human_size} received"
    fi
  done

  # Close fifo
  exec 3>&-

  # Kill websocat if still running
  if kill -0 "$websocat_pid" 2>/dev/null; then
    kill "$websocat_pid" 2>/dev/null || true
    wait "$websocat_pid" 2>/dev/null || true
  fi

  local timed_out=false
  if [[ "$snapshot_complete" != "true" ]]; then
    timed_out=true
    echo "" >> "$log_file"
    echo "Timeout waiting for heap snapshot (waited ${wait_time}s)" >> "$log_file"
    log_warn "Heap snapshot via inspector timed out after ${wait_time}s - attempting partial extraction"
  fi

  # Step 6: Extract heap snapshot chunks from the collected data
  # Try extraction even on timeout - we may have partial but useful data
  echo "" >> "$log_file"
  echo "=== Extracting Heap Snapshot ===" >> "$log_file"
  if [[ "$timed_out" == "true" ]]; then
    log_info "Attempting to extract partial heap snapshot data..."
  else
    log_info "Extracting heap snapshot data..."
  fi

  local chunks_received=0
  local raw_size
  raw_size=$(stat -c%s "$outfile" 2>/dev/null || echo 0)
  echo "Raw WebSocket data size: $raw_size bytes" >> "$log_file"

  # WebSocket output may contain leading null bytes from buffer initialization.
  # Strip them before JSON parsing. The messages are already newline-separated.
  local cleaned_file="$inspector_temp_dir/inspector_cleaned_$$"
  if ! tr -d '\0' < "$outfile" > "$cleaned_file" 2>> "$log_file"; then
    echo "Failed to strip null bytes from output" >> "$log_file"
    log_warn "Failed to process WebSocket output"
    return 1
  fi

  local cleaned_size
  cleaned_size=$(stat -c%s "$cleaned_file" 2>/dev/null || echo 0)
  echo "Cleaned data size (null bytes removed): $cleaned_size bytes" >> "$log_file"

  # Use -rj: -r for raw string output (chunks are already valid JSON content),
  #         -j to join outputs without adding newlines between chunks.
  if ! jq -rj 'select(.method == "HeapProfiler.addHeapSnapshotChunk") | .params.chunk // empty' "$cleaned_file" > "$heapfile" 2>> "$log_file"; then
    echo "jq extraction failed" >> "$log_file"
    echo "First 200 bytes after cleaning:" >> "$log_file"
    head -c 200 "$cleaned_file" | xxd >> "$log_file" 2>&1 || true
    log_warn "Failed to extract heap snapshot chunks"
    cleanup_port_forward
    rm -f "$fifo" "$outfile" "$heapfile" "$cleaned_file"
    return 1
  fi
  rm -f "$cleaned_file"

  # Count chunks for logging (use grep -o to count all occurrences, not just lines)
  chunks_received=$(grep -ao '"HeapProfiler.addHeapSnapshotChunk"' "$outfile" 2>/dev/null | wc -l || echo 0)

  {
    echo ""
    echo "=== Collection Summary ==="
    echo "Chunks received: $chunks_received"
    echo "Time elapsed: ${wait_time}s"
    if [[ "$timed_out" == "true" ]]; then
      echo "Status: PARTIAL (timed out before completion)"
    else
      echo "Status: COMPLETE"
    fi
  } >> "$log_file"

  # Verify we got actual data
  local heap_size
  heap_size=$(stat -c%s "$heapfile" 2>/dev/null || echo "0")

  # If we timed out, try fallback regardless of whether we got partial data
  if [[ "$timed_out" == "true" ]]; then
    if [[ "$heap_size" -lt 1000 ]]; then
      {
        echo "Heap snapshot file too small ($heap_size bytes)"
        echo "This may indicate:"
        echo "  - WebSocket connection issues (chunks not received)"
        echo "  - Inspector protocol errors"
        echo "Attempting fallback method..."
      } >> "$log_file"
      log_warn "No heap data received via streaming - trying fallback"
    else
      # Save partial snapshot
      if mv "$heapfile" "$output_file" 2>> "$log_file"; then
        local partial_file="${output_file%.heapsnapshot}.PARTIAL.heapsnapshot"
        mv "$output_file" "$partial_file" 2>> "$log_file" || true
        local human_size
        human_size=$(du -h "$partial_file" 2>/dev/null | cut -f1)
        echo "PARTIAL heap snapshot saved: $partial_file ($human_size)" >> "$log_file"
        log_warn "Partial heap dump saved ($human_size) - may be incomplete"
      fi
    fi

    # Fallback: Try v8.writeHeapSnapshot() via Runtime.evaluate
    # This writes directly to a file in the container, bypassing WebSocket streaming
    echo "" >> "$log_file"
    echo "=== Fallback: v8.writeHeapSnapshot() ===" >> "$log_file"
    log_info "Attempting fallback: writing heap dump directly to container filesystem..."

    # Re-establish port-forward (inspector connection may have died)
    echo "Re-establishing inspector connection for fallback..." >> "$log_file"
    cleanup_port_forward

    # Send SIGUSR1 to ensure inspector is active
    log_debug "Sending SIGUSR1 to reactivate inspector..."
    send_signal_to_process "$ns" "$pod" "$container" "$node_pid" "USR1" 2>> "$log_file" || true
    sleep 2

    # Start new port-forward on a different port
    local fallback_port=$((local_port + 100))
    $KUBECTL_CMD port-forward -n "$ns" "pod/$pod" "$fallback_port:$inspector_port" >> "$log_file" 2>&1 &
    port_forward_pid=$!

    # Wait for port-forward to be ready
    local pf_wait=0
    while ! curl -s "http://localhost:$fallback_port/json" >/dev/null 2>&1; do
      sleep 0.5
      pf_wait=$((pf_wait + 1))
      if [[ $pf_wait -gt 20 ]] || ! kill -0 "$port_forward_pid" 2>/dev/null; then
        echo "Failed to re-establish port-forward for fallback" >> "$log_file"
        log_warn "Fallback failed: could not re-establish inspector connection"
        cleanup_port_forward
        return 0
      fi
    done
    echo "Port-forward re-established on port $fallback_port" >> "$log_file"

    # Get new WebSocket URL
    local fallback_ws_url
    fallback_ws_url=$(curl -s "http://localhost:$fallback_port/json" | jq -r '.[0].webSocketDebuggerUrl' 2>/dev/null)
    fallback_ws_url=$(echo "$fallback_ws_url" | sed "s|ws://[^:]*:|ws://localhost:|" | sed "s|:$inspector_port/|:$fallback_port/|")
    echo "Fallback WebSocket URL: $fallback_ws_url" >> "$log_file"

    local remote_heap_file="${HEAP_DUMP_REMOTE_DIR:-/tmp}/heapdump-fallback-$$.heapsnapshot"
    local fallback_fifo="$inspector_temp_dir/inspector_fallback_fifo_$$"
    local fallback_out="$inspector_temp_dir/inspector_fallback_out_$$"
    rm -f "$fallback_fifo" "$fallback_out" 2>/dev/null || true

    if ! mkfifo "$fallback_fifo" 2>> "$log_file" || ! touch "$fallback_out" 2>> "$log_file"; then
      echo "Failed to create fallback temp files" >> "$log_file"
      log_warn "Fallback failed: could not create temp files"
      return 0  # Return success since partial file was saved
    fi

    # Start new websocat connection for fallback
    websocat -t -B "$ws_buffer_size" "$fallback_ws_url" < "$fallback_fifo" > "$fallback_out" 2>> "$log_file" &
    local fallback_ws_pid=$!
    sleep 0.5

    if kill -0 "$fallback_ws_pid" 2>/dev/null; then
      exec 4>"$fallback_fifo"

      # Use Runtime.evaluate to write heap snapshot to file
      # includeCommandLineAPI:true provides a require() function in the inspector context
      # that works regardless of whether the app uses CommonJS or ESM
      local write_cmd="require('v8').writeHeapSnapshot('$remote_heap_file')"
      echo "Sending: Runtime.evaluate with $write_cmd" >> "$log_file"
      echo "{\"id\":10,\"method\":\"Runtime.evaluate\",\"params\":{\"expression\":\"$write_cmd\",\"includeCommandLineAPI\":true,\"returnByValue\":true}}" >&4

      # Wait for response (honor configured timeout)
      local fallback_wait=0
      local fallback_max=$inspector_timeout
      local fallback_done=false

      while [[ "$fallback_done" != "true" && $fallback_wait -lt $fallback_max ]]; do
        sleep 1
        fallback_wait=$((fallback_wait + 1))

        if grep -qa '"id":10' "$fallback_out" 2>/dev/null; then
          fallback_done=true
        fi

        # Log progress every 30s
        if [[ $((fallback_wait % 30)) -eq 0 ]]; then
          log_info "Waiting for v8.writeHeapSnapshot()... ${fallback_wait}s"
        fi
      done

      exec 4>&-
      kill "$fallback_ws_pid" 2>/dev/null || true
      wait "$fallback_ws_pid" 2>/dev/null || true

      if [[ "$fallback_done" == "true" ]]; then
        # Check if the file was created and copy it
        local response
        response=$(grep -a '"id":10' "$fallback_out" | head -1)
        echo "Response: $response" >> "$log_file"

        # Check for errors in response
        if echo "$response" | grep -q '"error"'; then
          local err_msg
          err_msg=$(echo "$response" | jq -r '.error.message // "unknown error"' 2>/dev/null)
          echo "v8.writeHeapSnapshot() failed: $err_msg" >> "$log_file"
          log_warn "Fallback failed: $err_msg"
        else
          # Extract the returned filename (v8.writeHeapSnapshot returns the path)
          local returned_path
          returned_path=$(echo "$response" | jq -r '.result.result.value // empty' 2>/dev/null)
          if [[ -n "$returned_path" ]]; then
            remote_heap_file="$returned_path"
          fi
          echo "Heap snapshot written to: $remote_heap_file" >> "$log_file"

          # Copy the file from the container
          local fallback_output="${output_file%.heapsnapshot}.fallback.heapsnapshot"
          log_info "Copying heap dump from container..."
          if $KUBECTL_CMD cp -n "$ns" "${pod}:${remote_heap_file}" "$fallback_output" -c "$container" >> "$log_file" 2>&1; then
            local fallback_size
            fallback_size=$(du -h "$fallback_output" 2>/dev/null | cut -f1)
            echo "Fallback heap snapshot copied: $fallback_output ($fallback_size)" >> "$log_file"
            log_success "Fallback heap dump collected via v8.writeHeapSnapshot ($fallback_size)"

            # Clean up remote file
            $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- rm -f "$remote_heap_file" 2>/dev/null || true

            # Remove the partial file since we have a complete one
            if [[ -n "${partial_file:-}" && -f "$partial_file" ]]; then
              rm -f "$partial_file"
            fi
          else
            echo "Failed to copy heap snapshot from container" >> "$log_file"
            log_warn "Fallback failed: could not copy file from container"
          fi
        fi
      else
        echo "Timeout waiting for v8.writeHeapSnapshot() (${fallback_wait}s)" >> "$log_file"
        log_warn "Fallback timed out after ${fallback_wait}s"
      fi

      rm -f "$fallback_fifo" "$fallback_out"
    else
      echo "Failed to establish WebSocket connection for fallback" >> "$log_file"
      log_warn "Fallback failed: could not connect to inspector"
      rm -f "$fallback_fifo" "$fallback_out"
    fi

    cleanup_port_forward
    rm -f "$fifo" "$outfile"
    return 0
  fi

  # Success case: streaming completed without timeout
  if [[ "$heap_size" -lt 1000 ]]; then
    {
      echo "Heap snapshot file too small ($heap_size bytes)"
      echo "This may indicate:"
      echo "  - WebSocket connection issues (chunks not received)"
      echo "  - Inspector protocol errors"
      echo "  - Empty heap (unlikely for Backstage)"
    } >> "$log_file"
    log_warn "Heap snapshot appears empty or corrupted"
    return 1
  fi

  # Move the heap file to output location
  if ! mv "$heapfile" "$output_file" 2>> "$log_file"; then
    echo "Failed to move heap file to output location" >> "$log_file"
    log_warn "Failed to save heap snapshot"
    return 1
  fi

  local human_size
  human_size=$(du -h "$output_file" 2>/dev/null | cut -f1)
  echo "Heap snapshot saved: $output_file ($human_size)" >> "$log_file"
  log_success "Heap dump collected via inspector protocol ($human_size)"
  return 0
}

collect_heap_dumps_for_pods() {
  local ns="$1"
  local labels="$2"
  local output_dir="$3"
  local deploy_name="${4:-}"      # Deployment/StatefulSet name
  local instance_name="${5:-}"    # Helm release name or CR name (optional)
  local owner_kind="${6:-}"       # Optional: "deployment" or "statefulset" to filter by owner

  # Only collect heap dumps if explicitly enabled
  if [[ "${RHDH_WITH_HEAP_DUMPS:-false}" != "true" ]]; then
    log_debug "Heap dump collection disabled (use --with-heap-dumps to enable)"
    return 0
  fi

  # Check instance filter if specified
  # Match against deploy_name OR instance_name (Helm release or CR name)
  # Supports exact match, prefix match (e.g., "my-rhdh" matches "my-rhdh-backstage"), or contains match
  if [[ -n "${RHDH_HEAP_DUMP_INSTANCES:-}" ]]; then
    local match_found=false
    IFS=',' read -ra INSTANCES <<< "$RHDH_HEAP_DUMP_INSTANCES"
    for instance in "${INSTANCES[@]}"; do
      # Trim whitespace
      instance=$(echo "$instance" | xargs)
      # Check deploy_name: exact match, prefix match, or contains
      if [[ -n "$deploy_name" ]]; then
        if [[ "$deploy_name" == "$instance" ]] || \
           [[ "$deploy_name" == "$instance"-* ]] || \
           [[ "$deploy_name" == *"$instance"* ]]; then
          match_found=true
          break
        fi
      fi
      # Check instance_name: exact match, prefix match, or contains
      if [[ -n "$instance_name" ]]; then
        if [[ "$instance_name" == "$instance" ]] || \
           [[ "$instance_name" == "$instance"-* ]] || \
           [[ "$instance_name" == *"$instance"* ]]; then
          match_found=true
          break
        fi
      fi
    done
    if [[ "$match_found" != "true" ]]; then
      local names="${deploy_name}"
      [[ -n "$instance_name" && "$instance_name" != "$deploy_name" ]] && names="$names, $instance_name"
      log_debug "Skipping heap dump for instance(s) '$names' (not in filter: $RHDH_HEAP_DUMP_INSTANCES)"
      return 0
    fi
  fi

  log_info "Collecting heap dumps for pods with labels: $labels in namespace: $ns"
  
  local heap_dump_dir="$output_dir/heap-dumps"
  ensure_directory "$heap_dump_dir"
  
  # Timeout for heap dump generation (per pod)
  local HEAP_DUMP_TIMEOUT="${HEAP_DUMP_TIMEOUT:-600}"
  
  # Get list of running pods matching the labels, optionally filtered by owner kind
  local pods
  local _owner_ref_kind=""
  if [[ "$owner_kind" == "deployment" ]]; then
    _owner_ref_kind="ReplicaSet"
  elif [[ "$owner_kind" == "statefulset" ]]; then
    _owner_ref_kind="StatefulSet"
  fi

  if [[ -n "$_owner_ref_kind" ]]; then
    pods=$($KUBECTL_CMD get pods -n "$ns" -l "$labels" --field-selector=status.phase=Running -o json 2>/dev/null \
      | jq -r --arg ok "$_owner_ref_kind" \
        '.items[] | select(any(.metadata.ownerReferences[]?; .kind == $ok)) | .metadata.name' || true)
  else
    pods=$($KUBECTL_CMD get pods -n "$ns" -l "$labels" --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
  fi

  if [[ -z "$pods" ]]; then
    log_warn "No running pods found with labels: $labels in namespace: $ns"
    echo "No running pods found" > "$heap_dump_dir/no-pods.txt"
    return 0
  fi

  for pod in $pods; do
    log_info "Processing pod: $pod for heap dump collection"

    local pod_dir="$heap_dump_dir/pod=$pod"
    ensure_directory "$pod_dir"

    # Get pod spec
    $KUBECTL_CMD get pod -n "$ns" "$pod" -o yaml > "$pod_dir/pod-spec.yaml" 2>&1 || true

    # Pre-flight check: warn if liveness probe timeout is too short for heap dump collection
    _warn_liveness_probe_timeout "$ns" "$pod" "$HEAP_DUMP_TIMEOUT"

    # Find backstage-backend container
    local containers
    containers=$($KUBECTL_CMD get pod -n "$ns" "$pod" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || true)
    
    for container in $containers; do
      # Only process the backstage-backend container
      if [[ "$container" != "backstage-backend" ]]; then
        log_debug "Skipping container $container (only collecting from backstage-backend)"
        continue
      fi

      log_info "Processing backstage-backend container in pod: $pod"

      # Wrap entire container processing in error handling to ensure we continue on failure
      # This is a safety net for any unexpected errors
      if ! _process_container_heap_dump "$ns" "$pod" "$container" "$pod_dir" "$HEAP_DUMP_TIMEOUT"; then
        log_warn "Heap dump collection encountered an error for $pod/$container, continuing to next container"
        continue
      fi
    done
  done

  log_success "Heap dump collection completed for namespace: $ns"
}

# Pre-flight check: warn if liveness probe timeout is too short for heap dump collection
# Heap dumps block the Node.js event loop, which can cause liveness probe failures
_warn_liveness_probe_timeout() {
  local ns="$1"
  local pod="$2"
  local heap_timeout="$3"

  # Get liveness probe configuration for backstage-backend container
  local probe_json
  probe_json=$($KUBECTL_CMD get pod -n "$ns" "$pod" -o jsonpath='{.spec.containers[?(@.name=="backstage-backend")].livenessProbe}' 2>/dev/null || true)

  if [[ -z "$probe_json" || "$probe_json" == "{}" ]]; then
    log_debug "No liveness probe configured for pod $pod"
    return 0
  fi

  # Extract probe parameters (defaults per Kubernetes docs)
  local failure_threshold period_seconds
  failure_threshold=$(echo "$probe_json" | jq -r '.failureThreshold // 3' 2>/dev/null || echo "3")
  period_seconds=$(echo "$probe_json" | jq -r '.periodSeconds // 10' 2>/dev/null || echo "10")

  # Calculate effective timeout before pod restart
  local probe_timeout=$((failure_threshold * period_seconds))

  if [[ "$probe_timeout" -lt "$heap_timeout" ]]; then
    # Calculate recommended failureThreshold: ceil(heap_timeout / period_seconds)
    local recommended_threshold=$(( (heap_timeout + period_seconds - 1) / period_seconds ))

    log_warn "Pod '$pod' may restart during heap dump collection!"
    log_warn "  Current: failureThreshold=$failure_threshold × periodSeconds=${period_seconds}s = ${probe_timeout}s before restart"
    log_warn "  Required: at least ${heap_timeout}s (HEAP_DUMP_TIMEOUT)"
    log_warn ""
    log_warn "  Heap snapshots block the Node.js event loop, causing liveness probe failures."
    log_warn "  To prevent pod restarts, temporarily set failureThreshold >= $recommended_threshold before collecting:"
    log_warn "    kubectl patch deployment <name> -p '{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"backstage-backend\",\"livenessProbe\":{\"failureThreshold\":$recommended_threshold}}]}}}}'"
    log_warn ""
    log_warn "  See: https://github.com/redhat-developer/rhdh-must-gather/blob/main/docs/heap-dumps-collection.md#liveness-probe-considerations"
  fi
}

# Internal function to process heap dump for a single container
# Separated to allow proper error handling without affecting the main loop
_process_container_heap_dump() {
  local ns="$1"
  local pod="$2"
  local container="$3"
  local pod_dir="$4"
  local HEAP_DUMP_TIMEOUT="$5"
      
      # Find the Node.js process PID in the backstage-backend container
      # Using /proc filesystem as it's always available in Linux containers
      # (unlike ps, pidof, or pgrep which require additional packages)
      log_debug "Looking for Node.js process using /proc filesystem..."
      local node_pid
      local _pid_err=""
      node_pid=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "
        for pid_dir in /proc/[0-9]*; do
          pid=\$(basename \$pid_dir)
          # Check process name in comm file
          if [ -f \$pid_dir/comm ] && grep -qi node \$pid_dir/comm 2>/dev/null; then
            echo \$pid
            break
          fi
          # Check command line if comm didn't match
          if [ -f \$pid_dir/cmdline ] && grep -qi node \$pid_dir/cmdline 2>/dev/null; then
            echo \$pid
            break
          fi
        done
      " 2>&1) || _pid_err="$node_pid"
      
      if [[ -n "$_pid_err" ]]; then
        log_warn "Failed to exec into container $container in pod $pod: ${_pid_err:-unknown error}"
        local container_dir="$pod_dir/container=$container"
        ensure_directory "$container_dir"
        {
          echo "Failed to exec into container to find Node.js process"
          echo ""
          echo "=== Error Details ==="
          echo "${_pid_err:-No error output captured}"
        } > "$container_dir/no-node-process.txt"
        node_pid=""
        return 1
      fi

      if [[ -z "$node_pid" ]]; then
        log_warn "No Node.js process found in backstage-backend container"
        local container_dir="$pod_dir/container=$container"
        ensure_directory "$container_dir"
        echo "No Node.js process found in backstage-backend container" > "$container_dir/no-node-process.txt"
        echo "Searched /proc filesystem for node process" >> "$container_dir/no-node-process.txt"
        echo "This usually means the container is not running a Node.js application" >> "$container_dir/no-node-process.txt"
        return 1
      fi
      
      log_info "Found Node.js process (PID: $node_pid) in backstage-backend container"
      
      local container_dir="$pod_dir/container=$container"
      ensure_directory "$container_dir"

      local timestamp
      timestamp=$(date +%Y%m%d-%H%M%S)
      local heap_file="heapdump-${timestamp}.heapsnapshot"
      
      # Log the Node.js PID
      log_info "Node.js process PID: $node_pid"
      echo "Node.js PID: $node_pid" >> "$container_dir/heap-dump.log"
      
      # Collect process metadata
      {
        echo "=== Process Information ==="
        echo "PID: $node_pid"
        echo ""
        echo "Process Status (/proc/$node_pid/status):"
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "cat /proc/$node_pid/status 2>/dev/null || echo 'Could not read process status'"
        echo ""
        echo "Command Line (/proc/$node_pid/cmdline):"
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "cat /proc/$node_pid/cmdline 2>/dev/null | tr '\0' ' ' || echo 'Could not read command line'"
        echo ""
        echo "Environment (/proc/$node_pid/environ):"
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "cat /proc/$node_pid/environ 2>/dev/null | tr '\0' '\n' | grep -E '^(NODE_|PATH=)' || echo 'Could not read environment'"
        echo ""
        echo "=== Memory Usage ==="
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "cat /proc/meminfo 2>/dev/null || echo 'Could not get memory info'"
        echo ""
        echo "=== Node.js Version ==="
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- node --version 2>/dev/null || echo "Could not get Node.js version"
        echo ""
        echo "=== Available Disk Space ==="
        $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- df -h 2>/dev/null || echo "Could not get disk space"
      } > "$container_dir/process-info.txt"
      
      # Track whether heap dump was successfully collected
      local heap_collected=false
      local heap_dump_method="${RHDH_HEAP_DUMP_METHOD:-inspector}"

      log_info "Using heap dump method: $heap_dump_method"

      if [[ "$heap_dump_method" == "inspector" ]]; then
        # =====================================================================
        # Inspector Protocol Method
        # =====================================================================
        # Uses SIGUSR1 to activate inspector + Chrome DevTools Protocol
        # Benefits:
        # - Works even without --inspect flag (SIGUSR1 activates it dynamically)
        # - Provides feedback on success/failure
        # - Heap dump is collected directly via protocol

        log_info "Attempting heap dump collection via inspector protocol..."
        local inspector_heap_file="$container_dir/${heap_file}"

        if collect_heap_dump_via_inspector "$ns" "$pod" "$container" "$node_pid" \
             "$inspector_heap_file" "$container_dir/heap-dump.log" 2>&1; then
          heap_collected=true
          log_success "Heap dump collected via inspector protocol"
        else
          local inspector_error=$?
          log_warn "Inspector protocol method failed (exit code: $inspector_error)"
          echo "Inspector protocol failed with exit code: $inspector_error" >> "$container_dir/heap-dump.log"
        fi

      elif [[ "$heap_dump_method" == "sigusr2" ]]; then
        # =====================================================================
        # SIGUSR2 Signal Method
        # =====================================================================
        # This works if Node.js was started with --heapsnapshot-signal=SIGUSR2
        # or if the app has heapdump module or custom SIGUSR2 handler

        log_info "Sending SIGUSR2 signal to trigger heap dump..."

        # HEAP_DUMP_REMOTE_DIR should match --diagnostic-dir in NODE_OPTIONS
        local remote_dir="${HEAP_DUMP_REMOTE_DIR:-/tmp}"
        local search_paths="$remote_dir /tmp /app /opt/app-root/src"
        local poll_interval=5
        local max_wait="${HEAP_DUMP_TIMEOUT:-600}"
        # How long the file size must be stable (non-zero, unchanged) before considering it complete
        local stable_seconds="${HEAP_DUMP_SIGUSR2_STABLE_SECONDS:-150}"
        local waited=0
        local found_heap_file=""
        local last_size=0
        local stable_count=0

        {
          echo "Sending SIGUSR2 signal to Node.js process (PID: $node_pid)..."
          if send_signal_to_process "$ns" "$pod" "$container" "$node_pid" "USR2" 2>&1; then
            echo "SIGUSR2 sent successfully to PID $node_pid"
          else
            echo "Failed to send SIGUSR2 signal"
          fi
          echo ""
          echo "Polling for heap dump file (max ${max_wait}s, stable for ${stable_seconds}s)..."
        } >> "$container_dir/heap-dump.log" 2>&1

        # Poll for heap dump file and wait for it to be fully written
        # The file is created immediately but V8 writes to it over time
        # We wait until the file size is non-zero and stable for stable_seconds
        while [[ $waited -lt $max_wait ]]; do
          # Find heap dump file if not already found
          if [[ -z "$found_heap_file" ]]; then
            for search_path in $search_paths; do
              found_heap_file=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c \
                "find $search_path -maxdepth 2 -name '*.heapsnapshot' 2>/dev/null | head -1" 2>/dev/null || true)
              if [[ -n "$found_heap_file" ]]; then
                log_info "Found heap dump file: $found_heap_file (waiting for write to complete)"
                echo "Found heap dump file: $found_heap_file" >> "$container_dir/heap-dump.log"
                break
              fi
            done
          fi

          # If file found, check if it's fully written (size stable and non-zero)
          if [[ -n "$found_heap_file" ]]; then
            local current_size
            current_size=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c \
              "stat -c%s '$found_heap_file' 2>/dev/null || echo 0" 2>/dev/null || echo "0")

            if [[ "$current_size" -gt 0 ]]; then
              if [[ "$current_size" == "$last_size" ]]; then
                stable_count=$((stable_count + poll_interval))
                if [[ $stable_count -ge $stable_seconds ]]; then
                  log_info "Heap dump file size stable at ${current_size} bytes for ${stable_count}s"
                  echo "File size stable at ${current_size} bytes for ${stable_count}s - ready to copy" >> "$container_dir/heap-dump.log"
                  break
                fi
              else
                # Size changed, reset stability counter
                stable_count=0
                last_size="$current_size"
                log_debug "Heap dump still being written... (${current_size} bytes, ${waited}s elapsed)"
              fi
            fi
          fi

          sleep "$poll_interval"
          waited=$((waited + poll_interval))
          if [[ -z "$found_heap_file" ]] && (( waited % 30 == 0 )); then
            log_debug "Still waiting for heap dump file... (${waited}s elapsed)"
          fi
        done

        if [[ -n "$found_heap_file" && "$stable_count" -ge "$stable_seconds" ]]; then
          echo "Heap dump ready after ${waited}s total wait" >> "$container_dir/heap-dump.log"

          local local_path="$container_dir/${heap_file}"
          if $KUBECTL_CMD cp -n "$ns" "${pod}:${found_heap_file}" "$local_path" -c "$container" >> "$container_dir/heap-dump.log" 2>&1; then
            local file_size
            file_size=$(du -h "$local_path" 2>/dev/null | cut -f1)
            log_success "Heap dump copied to $local_path (${file_size})"
            echo "Heap dump collected: ${heap_file} (${file_size})" >> "$container_dir/heap-dump.log"

            # Clean up remote file
            $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- rm -f "$found_heap_file" 2>/dev/null || true

            heap_collected=true
          fi
        elif [[ -n "$found_heap_file" ]]; then
          echo "Heap dump file found but not stable after ${max_wait}s (last size: ${last_size}, stable for: ${stable_count}s)" >> "$container_dir/heap-dump.log"
          log_warn "Heap dump file found but write did not complete within timeout"
        else
          echo "No heap dump files found after ${max_wait}s in: $search_paths" >> "$container_dir/heap-dump.log"
        fi
      else
        log_error "Unknown heap dump method: $heap_dump_method"
        echo "Unknown heap dump method: $heap_dump_method" >> "$container_dir/heap-dump.log"
      fi

      # =====================================================================
      # Collection failed - provide guidance
      # =====================================================================
      if [[ "$heap_collected" != "true" ]]; then
        log_warn "Failed to collect heap dump for $pod/$container using method: $heap_dump_method"
        {
          echo "==================================================================="
          echo "Heap Dump Collection Failed"
          echo "==================================================================="
          echo ""
          echo "Method used: $heap_dump_method"
          echo ""
          echo "Node.js Process Information:"
          echo "  PID: $node_pid"
          echo "  Container: $container"
          echo "  Pod: $pod"
          echo "  Namespace: $ns"
          echo ""

          if [[ "$heap_dump_method" == "inspector" ]]; then
            echo "==================================================================="
            echo "Why Inspector Protocol Failed"
            echo "==================================================================="
            echo ""
            echo "Common reasons for failure:"
            echo ""
            echo "  - NODE_OPTIONS contains --disable-sigusr1 (prevents inspector activation)"
            echo "  - process.mainModule is not available (rare, ES modules edge case)"
            echo "  - Security policies blocking the inspector port"
            echo "  - Container doesn't have write access to /tmp"
            echo ""
            echo "Check heap-dump.log for detailed error messages."
            echo ""
            echo "==================================================================="
            echo "How to Fix"
            echo "==================================================================="
            echo ""
            echo "If --disable-sigusr1 is set, remove it or add --inspect explicitly:"
            echo ""
            echo "  env:"
            echo "  - name: NODE_OPTIONS"
            echo "    value: \"--inspect=0.0.0.0:9229\""
            echo ""
            echo "Alternatively, try the SIGUSR2 method:"
            echo ""
            echo "  ./gather --with-heap-dumps --heap-dump-method sigusr2"
            echo ""
            echo "Note: SIGUSR2 method requires NODE_OPTIONS configuration:"
            echo "  NODE_OPTIONS=\"--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp\""
          else
            echo "==================================================================="
            echo "Why SIGUSR2 Method Failed"
            echo "==================================================================="
            echo ""
            echo "SIGUSR2 method requires NODE_OPTIONS configuration:"
            echo ""
            echo "  env:"
            echo "  - name: NODE_OPTIONS"
            echo "    value: \"--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp\""
            echo ""
            echo "(--diagnostic-dir=/tmp is required for read-only root filesystems)"
            echo ""
            echo "Alternatively, try the inspector method (default, usually works without config):"
            echo ""
            echo "  ./gather --with-heap-dumps --heap-dump-method inspector"
          fi

          echo ""
          echo "==================================================================="
          echo "Diagnostic Logs"
          echo "==================================================================="
          echo ""
          echo "For detailed logs: heap-dump.log"
          echo "For process info: process-info.txt"
          echo ""
        } > "$container_dir/collection-failed.txt"

        log_info "Created guidance file: $container_dir/collection-failed.txt"
      fi

  # Return success - heap dump collection completed (whether successful or not)
  return 0
}

collect_rhdh_workload() {
  local ns="$1"
  local name="$2"
  local kind="$3"  # "deployment" or "statefulset"
  local output_dir="$4"
  local instance_name="${5:-}"

  log_debug "Collecting $kind $name in $ns"
  ensure_directory "$output_dir"

  local resource_path="${kind}s/$name"

  safe_exec "$KUBECTL_CMD -n '$ns' get $kind $name -o yaml" "$output_dir/$kind.yaml" "$kind for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' describe $kind $name" "$output_dir/$kind.describe.txt" "$kind description for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path -c backstage-backend --prefix ${log_collection_args:-}" "$output_dir/logs-app--backstage-backend.txt" "$kind backstage-backend logs for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path -c backstage-backend --prefix --previous ${log_collection_args:-}" "$output_dir/logs-app--backstage-backend-previous.txt" "$kind backstage-backend logs (previous) for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path -c install-dynamic-plugins --prefix ${log_collection_args:-}" "$output_dir/logs-app--install-dynamic-plugins.txt" "$kind init-container logs for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path -c install-dynamic-plugins --prefix --previous ${log_collection_args:-}" "$output_dir/logs-app--install-dynamic-plugins-previous.txt" "$kind init-container logs (previous) for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path --all-containers --prefix ${log_collection_args:-}" "$output_dir/logs-app.txt" "$kind logs for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs $resource_path --all-containers --prefix --previous ${log_collection_args:-}" "$output_dir/logs-app-previous.txt" "$kind logs (previous) for $ns/$name"

  local labels
  labels=$(
    $KUBECTL_CMD -n "$ns" get "$kind" "$name" -o json \
      | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")' || true
  )
  if [[ -n "$labels" ]]; then
    collect_rhdh_info_from_running_pods "$ns" "$labels" "$output_dir" "$kind"
    collect_heap_dumps_for_pods "$ns" "$labels" "$output_dir" "$name" "$instance_name" "$kind" || true

    # Filter pods by owner kind to avoid collecting pods from a different workload
    # that shares the same labels (e.g., during a Deployment-to-StatefulSet migration)
    local _owner_ref_kind=""
    if [[ "$kind" == "deployment" ]]; then
      _owner_ref_kind="ReplicaSet"
    elif [[ "$kind" == "statefulset" ]]; then
      _owner_ref_kind="StatefulSet"
    fi

    local pod_names
    if [[ -n "$_owner_ref_kind" ]]; then
      pod_names=$(
        $KUBECTL_CMD get pods -n "$ns" -l "$labels" -o json 2>/dev/null \
          | jq -r --arg ok "$_owner_ref_kind" \
            '.items[] | select(any(.metadata.ownerReferences[]?; .kind == $ok)) | .metadata.name' || true
      )
    fi

    local pods_dir="$output_dir/pods"
    ensure_directory "$pods_dir"

    if [[ -n "${pod_names:-}" ]]; then
      # Use filtered pod names for targeted collection
      # shellcheck disable=SC2086
      safe_exec "$KUBECTL_CMD -n '$ns' get pods $pod_names" "$pods_dir/pods.txt" "$kind pods for $ns/$name"
      # shellcheck disable=SC2086
      safe_exec "$KUBECTL_CMD -n '$ns' get pods $pod_names -o yaml" "$pods_dir/pods.yaml" "$kind pods YAML for $ns/$name"
      # shellcheck disable=SC2086
      safe_exec "$KUBECTL_CMD -n '$ns' describe pods $pod_names" "$pods_dir/pods.describe.txt" "$kind pods description for $ns/$name"
    else
      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels'" "$pods_dir/pods.txt" "$kind pods for $ns/$name"
      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels' -o yaml" "$pods_dir/pods.yaml" "$kind pods YAML for $ns/$name"
      safe_exec "$KUBECTL_CMD -n '$ns' describe pods -l '$labels'" "$pods_dir/pods.describe.txt" "$kind pods description for $ns/$name"
    fi
  fi
}

collect_rhdh_db_statefulset() {
  local ns="$1"
  local name="$2"
  local output_dir="$3"

  log_debug "db-statefulset=$name"
  if [[ -z "$name" ]]; then
    return 0
  fi

  local statefulset_dir="$output_dir/db-statefulset"
  ensure_directory "$statefulset_dir"

  safe_exec "$KUBECTL_CMD -n '$ns' get statefulset $name -o yaml" "$statefulset_dir/db-statefulset.yaml" "DB statefulset for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' describe statefulset $name" "$statefulset_dir/db-statefulset.describe.txt" "DB statefulset for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs statefulsets/$name --all-containers --prefix ${log_collection_args:-}" "$statefulset_dir/logs-db.txt" "DB StatefulSet logs for $ns/$name"
  safe_exec "$KUBECTL_CMD -n '$ns' logs statefulsets/$name --all-containers --prefix --previous ${log_collection_args:-}" "$statefulset_dir/logs-db-previous.txt" "DB StatefulSet logs (previous) for $ns/$name"

  local labels
  labels=$(
    $KUBECTL_CMD -n "$ns" get statefulset "$name" -o json \
      | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")' || true
  )
  if [[ -n "$labels" ]]; then
    local pods_dir="$statefulset_dir/pods"
    ensure_directory "$pods_dir"

    safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels'" "$pods_dir/pods.txt" "DB statefulset pods for $ns/$name"
    safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels' -o yaml" "$pods_dir/pods.yaml" "DB statefulset pods for $ns/$name"
    safe_exec "$KUBECTL_CMD -n '$ns' describe pods -l '$labels'" "$pods_dir/pods.describe.txt" "DB statefulset pods for $ns/$name"
  fi
}

collect_namespace_data() {
  local ns="$1"
  local ns_dir="$2"

  ensure_directory "$ns_dir"

  cm_dir="$ns_dir/_configmaps"
  ensure_directory "$cm_dir"
  cms=$($KUBECTL_CMD get configmaps -n "$ns" -o jsonpath="{.items[*].metadata.name}" 2>/dev/null || true)
  if [[ -n "$cms" ]]; then
    for cm in $cms; do
      safe_exec "$KUBECTL_CMD -n '$ns' get configmap '$cm' -o yaml" "$cm_dir/$cm.yaml" "CM $cm"
      safe_exec "$KUBECTL_CMD -n '$ns' describe configmap '$cm'" "$cm_dir/$cm.describe.txt" "Details of CM $cm"
    done
  fi

  # Only collect secrets if explicitly requested
  if [[ "${RHDH_WITH_SECRETS:-false}" == "true" ]]; then
    sec_dir="$ns_dir/_secrets"
    ensure_directory "$sec_dir"
    sec_list=$($KUBECTL_CMD get secrets -n "$ns" -o jsonpath="{.items[*].metadata.name}" 2>/dev/null || true)
    if [[ -n "$sec_list" ]]; then
      for sec in $sec_list; do
        safe_exec "$KUBECTL_CMD -n '$ns' get secret '$sec' -o yaml" "$sec_dir/$sec.yaml" "Secret $sec"
        safe_exec "$KUBECTL_CMD -n '$ns' describe secret '$sec'" "$sec_dir/$sec.describe.txt" "Details of Secret $sec"
      done
    fi
  else
    log_debug "Skipping secret collection for namespace $ns (use --with-secrets to collect)"
  fi
}

export_log_collection_args() {
	# validation of MUST_GATHER_SINCE and MUST_GATHER_SINCE_TIME is done by the
	# caller (oc adm must-gather) so it's safe to use the values as they are.
	log_collection_args=""
	log_debug "MUST_GATHER_SINCE=${MUST_GATHER_SINCE:-}"
	log_debug "MUST_GATHER_SINCE_TIME=${MUST_GATHER_SINCE_TIME:-}"

	if [ -n "${MUST_GATHER_SINCE:-}" ]; then
		log_collection_args=--since="${MUST_GATHER_SINCE}"
	fi
	if [ -n "${MUST_GATHER_SINCE_TIME:-}" ]; then
		log_collection_args=--since-time="${MUST_GATHER_SINCE_TIME}"
	fi

	# oc adm node-logs `--since` parameter is not the same as oc adm inspect `--since`.
	# it takes a simplified duration in the form of '(+|-)[0-9]+(s|m|h|d)' or
	# an ISO formatted time. since MUST_GATHER_SINCE and MUST_GATHER_SINCE_TIME
	# are formatted differently, we re-format them so they can be used
	# transparently by node-logs invocations.
	node_log_collection_args=""

	if [ -n "${MUST_GATHER_SINCE:-}" ]; then
		# shellcheck disable=SC2001
		since=$(echo "${MUST_GATHER_SINCE:-}" | sed 's/\([0-9]*[dhms]\).*/\1/')
		node_log_collection_args=--since="-${since}"
	fi
	if [ -n "${MUST_GATHER_SINCE_TIME:-}" ]; then
	  # shellcheck disable=SC2001
		iso_time=$(echo "${MUST_GATHER_SINCE_TIME}" | sed 's/T/ /; s/Z//')
		node_log_collection_args=--since="${iso_time}"
	fi
	export log_collection_args
	export node_log_collection_args
}

export_log_collection_args
