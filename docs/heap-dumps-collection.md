## Analyzing Heap Dumps

When heap dumps are collected using `--with-heap-dumps`, they can be analyzed using various tools to investigate memory leaks, high memory usage, and performance issues.

### Prerequisites for Heap Dump Collection

The must-gather tool supports two methods to collect heap dumps:

1. **Inspector Protocol (default)**: Uses the Node.js inspector and Chrome DevTools Protocol
2. **SIGUSR2 Signal**: Sends a signal to trigger heap snapshot

You can choose the method using `--heap-dump-method`:

```bash
# Use inspector protocol (default)
./gather --with-heap-dumps --heap-dump-method inspector

# Use SIGUSR2 signal
./gather --with-heap-dumps --heap-dump-method sigusr2
```

To collect heap dumps from specific instances only, use `--heap-dump-instances`:

```bash
# Collect from a specific Helm release
./gather --with-heap-dumps --heap-dump-instances my-rhdh

# Collect from multiple instances (Helm releases, CR names, or deployment names)
./gather --with-heap-dumps --heap-dump-instances my-rhdh,developer-hub

# Collect from a specific deployment
./gather --with-heap-dumps --heap-dump-instances backstage-developer-hub
```

By default (when `--heap-dump-instances` is not specified), heap dumps are collected from all instances.

#### Method 1: Inspector Protocol (Default - No Configuration Required)

The inspector protocol method **works out of the box** for most RHDH deployments without any configuration changes. The tool automatically:

1. Sends SIGUSR1 to the Node.js process to activate the inspector dynamically
2. Connects to the inspector via WebSocket
3. Triggers a heap snapshot using `v8.writeHeapSnapshot()`
4. Copies the heap dump file from the container

**Benefits:**
- No configuration changes required in most cases
- Provides direct feedback on collection success/failure
- Heap dump location is controlled by the must-gather tool (default: `/tmp`)

**Troubleshooting Large Heaps:**

The inspector method streams heap snapshot data over WebSocket. For very large heaps, you may need to increase the buffer size:

```bash
# Increase WebSocket buffer to 32MB (default: 16MB)
HEAP_DUMP_BUFFER_SIZE=33554432 ./gather --with-heap-dumps

# Or 64MB for very large heaps
HEAP_DUMP_BUFFER_SIZE=67108864 ./gather --with-heap-dumps
```

If heap dump collection shows progress reaching 100% but then stalls without completing, try increasing the buffer size. This can happen when Node.js tries to send a very large chunk that exceeds the buffer.

**Custom Heap Dump Location:**

By default, heap dumps are written to `/tmp` in the container. If `/tmp` is not writable or has limited space, you can override this:

```bash
# Write heap dumps to a different directory
HEAP_DUMP_REMOTE_DIR=/opt/app-root/src/tmp ./gather --with-heap-dumps
```

**Optional: Pre-enable Inspector**

If you prefer to have the inspector always enabled (e.g., for debugging), you can add:

```yaml
# In your Deployment or Backstage CR (optional)
spec:
  template:
    spec:
      containers:
      - name: backstage-backend
        env:
        - name: NODE_OPTIONS
          value: "--inspect=0.0.0.0:9229"
```

**Custom Inspector Port:**

If you use a non-default port (e.g., `--inspect=0.0.0.0:9230`), the must-gather tool will automatically detect it from the process command line or NODE_OPTIONS environment variable.

**When Configuration IS Required:**

If Node.js is started with the `--disable-sigusr1` flag, the dynamic inspector activation will not work. In this case, you **must** either:
1. Remove `--disable-sigusr1` from your NODE_OPTIONS, or
2. Explicitly add `--inspect=0.0.0.0:9229` to NODE_OPTIONS to enable the inspector at startup

**Reference:** [Node.js Inspector Protocol](https://nodejs.org/en/learn/diagnostics/memory/using-heap-snapshot#4-trigger-heap-snapshot-using-inspector-protocol)

#### Method 2: SIGUSR2 Signal

Use this method if the inspector protocol doesn't work in your environment. **This method requires configuration:**

```yaml
# In your Deployment or Backstage CR
spec:
  template:
    spec:
      containers:
      - name: backstage-backend
        env:
        - name: NODE_OPTIONS
          value: "--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"
```

**Important**: The `--diagnostic-dir=/tmp` flag is required because the root filesystem in RHDH containers is read-only. Without it, heap snapshots cannot be written to the default current working directory.

**Custom Heap Dump Location:**

If you use a different `--diagnostic-dir` in your NODE_OPTIONS, you must tell the must-gather tool where to look:

```bash
# If your NODE_OPTIONS uses --diagnostic-dir=/opt/app-root/src/tmp
HEAP_DUMP_REMOTE_DIR=/opt/app-root/src/tmp ./gather --with-heap-dumps --heap-dump-method sigusr2
```

**Reference:** [Node.js CLI Documentation](https://nodejs.org/docs/latest-v22.x/api/cli.html#--heapsnapshot-signalsignal)

### Heap Dump Files

Heap dumps are saved as `.heapsnapshot` files within each deployment/CR directory, right alongside the logs:

```
# For Helm deployments:
helm/releases/ns=my-ns/my-release/deployment/heap-dumps/
└── pod=backstage-xyz/
    └── container=backstage-backend/
        ├── heapdump-20250105-143022.heapsnapshot  (500MB)
        ├── process-info.txt
        ├── heap-dump.log
        └── pod-spec.yaml

# For Operator deployments:
operator/backstage-crs/ns=my-ns/my-backstage-cr/deployment/heap-dumps/
└── pod=backstage-my-backstage-cr-xyz/
    └── container=backstage-backend/
        ├── heapdump-20250105-143022.heapsnapshot  (500MB)
        ├── process-info.txt
        ├── heap-dump.log
        └── pod-spec.yaml
```

This structure makes it easy to correlate heap dumps with the corresponding logs and deployment information.

### Analysis Tools

#### 1. Chrome DevTools (Recommended)

Chrome DevTools provides a powerful, visual interface for analyzing heap snapshots:

```bash
# Open Chrome and navigate to DevTools
# 1. Open Chrome browser
# 2. Press F12 or Ctrl+Shift+I to open DevTools
# 3. Go to the "Memory" tab
# 4. Click "Load" button
# 5. Select the .heapsnapshot file from must-gather output

# Or use Chrome DevTools from command line
google-chrome --auto-open-devtools-for-tabs
```

**Chrome DevTools Features:**
- **Summary view**: Object types, counts, and sizes
- **Comparison view**: Compare multiple snapshots to find memory leaks
- **Containment view**: Object references and retention paths
- **Statistics**: Memory distribution by type

#### 2. MemLab (Facebook's Memory Leak Detector)

```bash
# Analyze heap snapshot
npx @memlab/cli analyze --help
```

### Common Analysis Workflows

#### Finding Memory Leaks

1. **Collect multiple snapshots over time** (optional, not done automatically):
   ```bash
   # Collect initial snapshot
   oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- /usr/bin/gather --with-heap-dumps
   
   # Wait 30 minutes for memory to grow
   # Collect second snapshot
   oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- /usr/bin/gather --with-heap-dumps
   ```

2. **Compare snapshots in Chrome DevTools**:
   - Load first snapshot
   - Take note of baseline memory usage
   - Load second snapshot
   - Use "Comparison" view to see what grew

3. **Look for growing object counts**:
   - Arrays that keep growing
   - Event listeners not being removed
   - Cached data not being cleaned up

#### Investigating High Memory Usage

1. **Load snapshot in Chrome DevTools**
2. **Sort by "Retained Size"** to find largest objects
3. **Check "Distance" column** to see how far objects are from GC roots
4. **Inspect retention paths** to understand why objects aren't being freed

### Heap Dump Metadata

Each heap dump collection includes metadata files:

- **`process-info.txt`**: Node.js version, process details, memory usage at collection time
- **`heap-dump.log`**: Collection logs, any errors or warnings
- **`pod-spec.yaml`**: Complete pod specification for context

### Important Warnings

- **Application pause**: During heap dump collection, the Node.js event loop is **paused** while the V8 engine writes the heap snapshot. For large heaps (1GB+), this can take 30-60+ seconds during which the application will not respond to requests. The application **automatically resumes** after the heap snapshot is written. Plan heap dump collection during maintenance windows or low-traffic periods.
- **Liveness probe failures**: See [Liveness Probe Considerations](#liveness-probe-considerations) below for important guidance on preventing pod restarts during heap dump collection.
- **Inspector remains active**: When using the inspector method, SIGUSR1 activates the Node.js inspector which remains active after heap dump collection. This is harmless but means the inspector port stays open until the pod is restarted.
- **Timeout for large heaps**: The default `HEAP_DUMP_TIMEOUT` is 600 seconds (10 minutes). For very large heaps (multi-GB), the `v8.writeHeapSnapshot()` call may exceed this timeout. See [Overriding HEAP_DUMP_TIMEOUT](#overriding-heap_dump_timeout) below.

### Liveness Probe Considerations

Heap snapshots are **stop-the-world** operations - V8 must pause the entire JavaScript event loop to capture a consistent memory state. This is a fundamental limitation of how V8 heap snapshots work and cannot be avoided.

**Why this matters:**

When the event loop is paused:
- The application cannot respond to HTTP requests
- Liveness probe health checks will fail
- If probes fail long enough, Kubernetes will restart the pod

**Default RHDH probe configuration:**

```yaml
livenessProbe:
  failureThreshold: 3
  periodSeconds: 10
  timeoutSeconds: 4
```

With these defaults, the pod will restart after ~30 seconds of unresponsiveness (`failureThreshold × periodSeconds = 3 × 10 = 30s`). For heaps larger than ~500MB, heap dump collection often exceeds this threshold.

**The must-gather tool will warn you** if it detects that a pod's liveness probe timeout is shorter than the configured `HEAP_DUMP_TIMEOUT`:

```
[WARN] Pod 'backstage-xyz' may restart during heap dump collection!
[WARN]   Current: failureThreshold=3 × periodSeconds=10s = 30s before restart
[WARN]   Required: at least 600s (HEAP_DUMP_TIMEOUT)
[WARN]
[WARN]   Heap snapshots block the Node.js event loop, causing liveness probe failures.
[WARN]   To prevent pod restarts, temporarily set failureThreshold >= 60 before collecting:
[WARN]     kubectl patch deployment <name> -p '{"spec":{"template":{"spec":{"containers":[{"name":"backstage-backend","livenessProbe":{"failureThreshold":60}}]}}}}'
```

**Solution: Temporarily increase liveness probe threshold**

Before collecting heap dumps, increase `failureThreshold` to allow enough time for the heap snapshot to complete. For example, to allow 10 minutes (60 × 10s = 600s):

```bash
# Increase failureThreshold to allow 10 minutes
kubectl patch deployment backstage-developer-hub -p '{"spec":{"template":{"spec":{"containers":[{"name":"backstage-backend","livenessProbe":{"failureThreshold":60}}]}}}}'

# Collect heap dumps
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- /usr/bin/gather --with-heap-dumps

# Restore original configuration
kubectl patch deployment backstage-developer-hub -p '{"spec":{"template":{"spec":{"containers":[{"name":"backstage-backend","livenessProbe":{"failureThreshold":3}}]}}}}'
```

**For Operator-managed deployments**, patch the Backstage CR or the generated Deployment directly.

**Why only the liveness probe?** The readiness probe does not need to be adjusted. When the readiness probe fails, the pod is removed from Service endpoints (stops receiving traffic) but continues running. This is expected during heap dump collection. Once the heap dump completes, the readiness probe passes again and traffic resumes. The liveness probe is what matters - if it fails, Kubernetes restarts the pod and you lose the heap dump.

**Tip:** For multi-replica deployments, you may want to collect from one pod at a time using `--heap-dump-instances` to minimize impact. However, you still need to increase the liveness probe threshold - otherwise the pod will restart and you'll lose the heap dump.

**Note:** There is no way to take a V8 heap snapshot without pausing the event loop. This is a fundamental constraint of how JavaScript memory snapshots work - the heap must be in a consistent state to be captured accurately.

### Environment Variables

The following environment variables can be used to configure heap dump collection:

| Variable | Default | Description |
|----------|---------|-------------|
| `HEAP_DUMP_TIMEOUT` | `600` | Timeout in seconds for heap dump collection |
| `HEAP_DUMP_BUFFER_SIZE` | `16777216` | WebSocket buffer size in bytes (16MB) for inspector method |
| `HEAP_DUMP_REMOTE_DIR` | `/tmp` | Directory in container where heap dumps are written |

For very large heaps that take longer than 10 minutes to serialize, you can increase the timeout:

#### OpenShift (oc adm must-gather)

```bash
# Set timeout to 15 minutes (900 seconds)
oc adm must-gather \
  --image=quay.io/rhdh-community/rhdh-must-gather \
  -- /usr/bin/gather --with-heap-dumps \
  env HEAP_DUMP_TIMEOUT=900
```

Or using the `env` command:

```bash
oc adm must-gather \
  --image=quay.io/rhdh-community/rhdh-must-gather \
  -- env HEAP_DUMP_TIMEOUT=900 /usr/bin/gather --with-heap-dumps
```

#### Standard Kubernetes (Helm chart)

Set the environment variable via Helm values:

```bash
# Using a values file
cat > values.yaml <<EOF
gather:
  heapDump:
    enabled: true
    timeout: 900
EOF
helm install my-rhdh-must-gather redhat-developer-hub-must-gather \
  --repo https://redhat-developer.github.io/rhdh-chart \
  -f values.yaml
```

See the [chart documentation](https://github.com/redhat-developer/rhdh-chart/tree/main/charts/must-gather) for all available options.

#### Local execution

```bash
HEAP_DUMP_TIMEOUT=900 ./collection-scripts/must_gather --with-heap-dumps
```

### Tips and Best Practices

- **Large files**: Heap dumps can be 100MB-1GB+. Ensure sufficient disk space and bandwidth for analysis.
- **Privacy**: Heap dumps may contain sensitive data from memory. Handle them securely and apply sanitization if sharing.
- **Timing**: Collect heap dumps when memory usage is high or after OOM events for best results.
- **Comparison**: Multiple snapshots over time help identify memory leaks vs. normal memory growth.
- **Node.js version**: Ensure your analysis tools support the Node.js version used by the application.
- **Collection methods**: The tool tries two approaches in order: (1) Inspector Protocol via SIGUSR1 + port-forward + websocat, then (2) SIGUSR2 signal. Success depends on application setup - see Prerequisites above.
- **Troubleshooting failures**: If collection fails, check `heap-dump.log` and `collection-failed.txt` for specific guidance on enabling heap dumps.
