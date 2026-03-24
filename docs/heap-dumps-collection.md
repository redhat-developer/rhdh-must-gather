## Analyzing Heap Dumps

When heap dumps are collected using `--with-heap-dumps`, they can be analyzed using various tools to investigate memory leaks, high memory usage, and performance issues.

### Prerequisites for Heap Dump Collection

The must-gather tool uses two methods to collect heap dumps, tried in order:

1. **Inspector Protocol (Primary)**: Uses the Node.js inspector and Chrome DevTools Protocol
2. **SIGUSR2 Signal (Fallback)**: Sends a signal to trigger heap snapshot

#### Option 1: Inspector Protocol (Primary - No Configuration Required)

The inspector protocol method **works out of the box** for most RHDH deployments without any configuration changes. The tool automatically:

1. Sends SIGUSR1 to the Node.js process to activate the inspector dynamically
2. Connects to the inspector via WebSocket
3. Triggers a heap snapshot using `v8.writeHeapSnapshot()`
4. Copies the heap dump file from the container

**Benefits:**
- No configuration changes required in most cases
- Provides direct feedback on collection success/failure
- Heap dump location is controlled by the must-gather tool

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

#### Option 2: SIGUSR2 Signal (Fallback)

If the inspector protocol fails, the tool falls back to sending SIGUSR2. **This method requires configuration:**

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

- **Application pause**: During heap dump collection, the Node.js event loop is **paused** while the V8 engine writes the heap snapshot. For large heaps (1GB+), this can take 30-60+ seconds during which the application will not respond to requests. Plan heap dump collection during maintenance windows or low-traffic periods.
- **Timeout for large heaps**: The default `HEAP_DUMP_TIMEOUT` is 600 seconds (10 minutes). For very large heaps (multi-GB), the `v8.writeHeapSnapshot()` call may exceed this timeout. Increase the timeout by setting `HEAP_DUMP_TIMEOUT=900` (or higher) in your environment.

### Tips and Best Practices

- **Large files**: Heap dumps can be 100MB-1GB+. Ensure sufficient disk space and bandwidth for analysis.
- **Privacy**: Heap dumps may contain sensitive data from memory. Handle them securely and apply sanitization if sharing.
- **Timing**: Collect heap dumps when memory usage is high or after OOM events for best results.
- **Comparison**: Multiple snapshots over time help identify memory leaks vs. normal memory growth.
- **Node.js version**: Ensure your analysis tools support the Node.js version used by the application.
- **Collection methods**: The tool tries two approaches in order: (1) Inspector Protocol via SIGUSR1 + port-forward + websocat, then (2) SIGUSR2 signal. Success depends on application setup - see Prerequisites above.
- **Troubleshooting failures**: If collection fails, check `heap-dump.log` and `collection-failed.txt` for specific guidance on enabling heap dumps.
