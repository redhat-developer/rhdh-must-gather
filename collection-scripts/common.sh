#!/usr/bin/env bash

set -euo pipefail

trap 'log "An unexpected error occurred. See logs above."' ERR

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

export LOG_LEVEL="${LOG_LEVEL:-info}"
if [[ "$LOG_LEVEL" == "trace" ]]; then
  set -x
fi

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

    if ! $KUBECTL_CMD cluster-info >/dev/null 2>&1; then
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

    if ! timeout "$CMD_TIMEOUT" bash -c "$cmd" > "$output_file" 2>&1; then
        log_warn "\tCommand timed out or failed: $cmd"
        echo "Command failed or timed out: $cmd" > "$output_file"
        echo "Timestamp: $(date)" >> "$output_file"
        echo "Timeout: ${CMD_TIMEOUT}s" >> "$output_file"
        #return 1
    fi

    #return 0
}

collect_rhdh_info_from_running_pods() {
  local ns="$1"
  local labels="$2"
  local output_dir="$3"

  # Get all running pods matching the labels
  local running_pods
  running_pods=$(
    $KUBECTL_CMD get pods -n "$ns" \
      -l "$labels" \
      -o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}'
  )

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
  $KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c '
    echo "=== RHDH/Backstage Environment Variables ==="
    echo ""
    env | grep -E "^(BACKSTAGE_|RHDH_|UPSTREAM_REPO|MIDSTREAM_REPO|NODE_|APP_CONFIG_|LOG_LEVEL|PLUGIN_|NO_PROXY|HTTP_PROXY|HTTPS_PROXY|NPM_CONFIG_|GLOBAL_AGENT_)" | sort || true
  ' > "$env_vars_file" 2>&1 || true

  # Extract specific env vars for version/metadata collection
  local backstage_version=""
  local rhdh_version=""
  local upstream_repo=""
  local midstream_repo=""

  # shellcheck disable=SC2016 # Variables are intentionally expanded inside the container, not on the host
  backstage_version=$($KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c 'echo "$BACKSTAGE_VERSION"' 2>/dev/null || true)
  # shellcheck disable=SC2016
  rhdh_version=$($KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c 'echo "$RHDH_VERSION"' 2>/dev/null || true)
  # shellcheck disable=SC2016
  upstream_repo=$($KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c 'echo "$UPSTREAM_REPO"' 2>/dev/null || true)
  # shellcheck disable=SC2016
  midstream_repo=$($KUBECTL_CMD -n "$ns" exec "$first_pod" -- sh -c 'echo "$MIDSTREAM_REPO"' 2>/dev/null || true)

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
      log_warn "Failed to collect processes from container $container"
      echo "Failed to collect processes from container $container" > "$container_file"
      echo "The container may not be running or may not have /proc mounted" >> "$container_file"
    fi
  done

  log_debug "Process collection completed for pod: $pod"
  return 0
}

collect_heap_dumps_for_pods() {
  local ns="$1"
  local labels="$2"
  local output_dir="$3"
  
  # Only collect heap dumps if explicitly enabled
  if [[ "${RHDH_WITH_HEAP_DUMPS:-false}" != "true" ]]; then
    log_debug "Heap dump collection disabled (use --with-heap-dumps to enable)"
    return 0
  fi
  
  log_info "Collecting heap dumps for pods with labels: $labels in namespace: $ns"
  
  local heap_dump_dir="$output_dir/heap-dumps"
  ensure_directory "$heap_dump_dir"
  
  # Timeout for heap dump generation (per pod)
  local HEAP_DUMP_TIMEOUT="${HEAP_DUMP_TIMEOUT:-120}"
  
  # Get list of running pods matching the labels
  local pods
  pods=$($KUBECTL_CMD get pods -n "$ns" -l "$labels" --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
  
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
      
      # Find the Node.js process PID in the backstage-backend container
      # Using /proc filesystem as it's always available in Linux containers
      # (unlike ps, pidof, or pgrep which require additional packages)
      log_debug "Looking for Node.js process using /proc filesystem..."
      local node_pid
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
      " 2>/dev/null || true)
      
      if [[ -z "$node_pid" ]]; then
        log_warn "No Node.js process found in backstage-backend container"
        local container_dir="$pod_dir/container=$container"
        ensure_directory "$container_dir"
        echo "No Node.js process found in backstage-backend container" > "$container_dir/no-node-process.txt"
        echo "Searched /proc filesystem for node process" >> "$container_dir/no-node-process.txt"
        echo "This usually means the container is not running a Node.js application" >> "$container_dir/no-node-process.txt"
        continue
      fi
      
      log_info "Found Node.js process (PID: $node_pid) in backstage-backend container"
      
      local container_dir="$pod_dir/container=$container"
      ensure_directory "$container_dir"
      
      local timestamp=$(date +%Y%m%d-%H%M%S)
      local heap_file="heapdump-${timestamp}.heapsnapshot"
      local remote_path="/tmp/${heap_file}"
      
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
      
      # Send SIGUSR2 signal directly to the Node.js process
      # This works if Node.js was started with --heapsnapshot-signal=SIGUSR2 (recommended)
      # or if the app has heapdump module or custom SIGUSR2 handler
      log_info "Sending SIGUSR2 signal to trigger heap dump..."
      
      {
        echo "Sending SIGUSR2 signal to Node.js process (PID: $node_pid)..."
        if $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c "kill -USR2 $node_pid" 2>&1; then
          echo "✓ SIGUSR2 sent successfully to PID $node_pid"
        else
          echo "✗ Failed to send SIGUSR2 signal"
        fi
        
        # Wait for heap dump file to be created
        echo ""
        echo "Waiting ${HEAP_DUMP_TIMEOUT}s for heap dump to be generated..."
        sleep "${HEAP_DUMP_TIMEOUT}"
        
        # Look for heap dump files in common locations
        echo "Searching for heap dump files..."
        local found_dumps
        found_dumps=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c \
          "find /tmp /app /opt/app-root/src . -maxdepth 2 \( -name '*.heapsnapshot' -o -name 'Heap.*.heapsnapshot' -o -name 'heapdump-*.heapsnapshot' \) 2>/dev/null | head -5" 2>/dev/null || true)
        
        if [[ -n "$found_dumps" ]]; then
          echo "✓ Found heap dump file(s):"
          echo "$found_dumps"
        else
          echo "✗ No heap dump files found in /tmp, /app, /opt/app-root/src, or current directory"
        fi
      } >> "$container_dir/heap-dump.log" 2>&1
      
      # Try to copy any heap dump file we can find
      local copied=false
      local search_paths="/tmp /app /opt/app-root/src"
      
      for search_path in $search_paths; do
        local heap_files
        heap_files=$($KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- sh -c \
          "find $search_path -maxdepth 2 -name '*.heapsnapshot' 2>/dev/null | head -1" 2>/dev/null || true)
        
        if [[ -n "$heap_files" ]]; then
          log_info "Found heap dump file: $heap_files"
          
          local local_path="$container_dir/${heap_file}"
          if $KUBECTL_CMD cp -n "$ns" "${pod}:${heap_files}" "$local_path" -c "$container" >> "$container_dir/heap-dump.log" 2>&1; then
            local file_size=$(du -h "$local_path" 2>/dev/null | cut -f1)
            log_success "Heap dump copied to $local_path (${file_size})"
            echo "Heap dump collected: ${heap_file} (${file_size})" >> "$container_dir/heap-dump.log"
            
            # Clean up remote file
            $KUBECTL_CMD exec -n "$ns" "$pod" -c "$container" -- rm -f "$heap_files" 2>/dev/null || true
            
            copied=true
            break
          fi
        fi
      done
      
      if [[ "$copied" != "true" ]]; then
        log_warn "Failed to collect heap dump for $pod/$container"
        log_info "The application is not instrumented to generate heap dumps on SIGUSR2"
        {
          echo "==================================================================="
          echo "Heap Dump Collection Failed"
          echo "==================================================================="
          echo ""
          echo "All collection methods were attempted, but no heap dump was generated."
          echo ""
          echo "Node.js Process Information:"
          echo "  PID: $node_pid"
          echo "  Container: $container"
          echo "  Pod: $pod"
          echo "  Namespace: $ns"
          echo ""
          echo "Method Attempted:"
          echo "  ✓ SIGUSR2 signal sent directly to backstage-backend container (PID: $node_pid)"
          echo ""
          echo "Result: No heap dump files were created in /tmp, /app, /opt/app-root/src, or current directory"
          echo ""
          echo "==================================================================="
          echo "Why This Happened"
          echo "==================================================================="
          echo ""
          echo "The Backstage application is not currently instrumented to handle"
          echo "SIGUSR2 signals for heap dump generation. This is the default state"
          echo "for most Node.js applications."
          echo ""
          echo "==================================================================="
          echo "How to Enable Heap Dumps"
          echo "==================================================================="
          echo ""
          echo "⭐ Node.js Built-in Flag (RECOMMENDED)"
          echo "---------------------------------------------------------"
          echo "Built into Node.js v12.0.0+, no image rebuild or dependencies required!"
          echo ""
          echo "Add to your Deployment or Backstage CR:"
          echo "  spec:"
          echo "    template:"
          echo "      spec:"
          echo "        containers:"
          echo "        - name: backstage-backend"
          echo "          env:"
          echo "          - name: NODE_OPTIONS"
          echo "            value: \"--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp\""
          echo ""
          echo "⚠️  IMPORTANT:"
          echo "   • --heapsnapshot-signal=SIGUSR2 enables automatic heap dump on signal"
          echo "   • --diagnostic-dir=/tmp is REQUIRED for read-only root filesystems"
          echo "     (common security best practice). Without it, heap dumps will fail!"
          echo ""
          echo "Advantages:"
          echo "  ✅ Built into Node.js - zero dependencies!"
          echo "  ✅ No image rebuild required"
          echo "  ✅ No source code changes needed"
          echo "  ✅ Works immediately after pod restart"
          echo ""
          echo "Collection method: SIGUSR2 signal sent via kubectl exec"
          echo "Works with any Kubernetes version, no special RBAC permissions needed"
          echo ""
          echo "Reference: https://nodejs.org/docs/latest/api/cli.html#--heapsnapshot-signalsignal"
          echo ""
          echo "==================================================================="
          echo "Next Steps"
          echo "==================================================================="
          echo ""
          echo "1. Update your Deployment/CR with NODE_OPTIONS as shown above"
          echo "2. Redeploy and wait for the pod to restart"
          echo "3. Run must-gather again with --with-heap-dumps:"
          echo ""
          echo "   oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- \\"
          echo "     /usr/bin/gather --with-heap-dumps"
          echo ""
          echo "The heap dump will be automatically collected and included in the output."
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
    done
  done
  
  log_success "Heap dump collection completed for namespace: $ns"
}

collect_rhdh_data() {
  local ns="$1"
  local deploy="$2"
  local statefulset="$3"
  local output_dir="$4"

  log_debug "deploy=$deploy"
  if [[ -n "$deploy" ]]; then
    local deploy_dir="${output_dir}/deployment"
    ensure_directory "$deploy_dir"

    safe_exec "$KUBECTL_CMD -n '$ns' get deployment $deploy -o yaml || $KUBECTL_CMD -n '$ns' get statefulset $deploy -o yaml" "$deploy_dir/deployment.yaml" "app deployment for $ns/$deploy"
    safe_exec "$KUBECTL_CMD -n '$ns' describe deployment $deploy || $KUBECTL_CMD -n '$ns' describe statefulset $deploy" "$deploy_dir/deployment.describe.txt" "app deployment for $ns/$deploy"
    safe_exec "$KUBECTL_CMD -n '$ns' logs deployments/$deploy -c install-dynamic-plugins ${log_collection_args:-} || $KUBECTL_CMD -n '$ns' logs statefulsets/$deploy -c install-dynamic-plugins ${log_collection_args:-}" "$deploy_dir/logs-app--install-dynamic-plugins.txt" "app init-container logs for $ns/$deploy"
    safe_exec "$KUBECTL_CMD -n '$ns' logs deployments/$deploy -c backstage-backend ${log_collection_args:-} || $KUBECTL_CMD -n '$ns' logs statefulsets/$deploy -c backstage-backend ${log_collection_args:-}" "$deploy_dir/logs-app--backstage-backend.txt" "app backstage-backend logs for $ns/$deploy"
    safe_exec "$KUBECTL_CMD -n '$ns' logs deployments/$deploy --all-containers ${log_collection_args:-} || $KUBECTL_CMD -n '$ns' logs statefulsets/$deploy --all-containers ${log_collection_args:-}" "$deploy_dir/logs-app.txt" "app deployment logs for $ns/$deploy"
  
    labels=$(
      $KUBECTL_CMD -n "$ns" get deployment "$deploy" -o json \
        | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")'  || true
    )
     if [[ -z "$labels" ]]; then
      log_debug "No labels found for deployment $deploy, trying statefulset"
      labels=$(
        $KUBECTL_CMD -n "$ns" get statefulset "$deploy" -o json \
          | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")' || true
      )
     fi
    if [[ -n "$labels" ]]; then
      # Retrieve some information from the running pods
      collect_rhdh_info_from_running_pods "$ns" "$labels" "$deploy_dir"

      # Collect heap dumps right after collecting logs (if enabled)
      collect_heap_dumps_for_pods "$ns" "$labels" "$deploy_dir"

      pods_dir="$deploy_dir/pods"
      ensure_directory "$pods_dir"

      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels'" "$pods_dir/pods.txt" "app deployment pods for $ns/$deploy"
      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels' -o yaml" "$pods_dir/pods.yaml" "app deployment pods for $ns/$deploy"
      safe_exec "$KUBECTL_CMD -n '$ns' describe pods -l '$labels'" "$pods_dir/pods.describe.txt" "app deployment pods for $ns/$deploy"
    fi
  fi

  log_debug "statefulset=$statefulset"
  if [[ -n "$statefulset" ]]; then
    statefulset_dir="$output_dir/db-statefulset"
    ensure_directory "$statefulset_dir"

    safe_exec "$KUBECTL_CMD -n '$ns' get statefulset $statefulset -o yaml" "$statefulset_dir/db-statefulset.yaml" "DB statefulset for $ns/$statefulset"
    safe_exec "$KUBECTL_CMD -n '$ns' describe statefulset $statefulset" "$statefulset_dir/db-statefulset.describe.txt" "DB statefulset for $ns/$statefulset"
    safe_exec "$KUBECTL_CMD -n '$ns' logs statefulsets/$statefulset --all-containers ${log_collection_args:-}" "$statefulset_dir/logs-db.txt" "DB StatefulSet logs for $ns/$statefulset"

    labels=$(
      $KUBECTL_CMD -n "$ns" get statefulset "$statefulset" -o json \
        | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")' || true
    )
    if [[ -n "$labels" ]]; then
      pods_dir="$statefulset_dir/pods"
      ensure_directory "$pods_dir"

      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels'" "$pods_dir/pods.txt" "DB statefulset pods for $ns/$statefulset"
      safe_exec "$KUBECTL_CMD -n '$ns' get pods -l '$labels' -o yaml" "$pods_dir/pods.yaml" "DB statefulset pods for $ns/$statefulset"
      safe_exec "$KUBECTL_CMD -n '$ns' describe pods -l '$labels'" "$pods_dir/pods.describe.txt" "DB statefulset pods for $ns/$statefulset"
    fi
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
