## Analyzing Heap Dumps

When heap dumps are collected using `--with-heap-dumps`, they can be analyzed using various tools to investigate memory leaks, high memory usage, and performance issues.

### Prerequisites for Heap Dump Collection

Use Node.js's built-in `--heapsnapshot-signal` flag (available since Node.js v12.0.0):

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

### Tips and Best Practices

- **Application instrumentation**: For reliable heap dump collection, instrument your Backstage application (see Prerequisites above)
- **Large files**: Heap dumps can be 100MB-1GB+. Ensure sufficient disk space and bandwidth for analysis.
- **Privacy**: Heap dumps may contain sensitive data from memory. Handle them securely and apply sanitization if sharing.
- **Timing**: Collect heap dumps when memory usage is high or after OOM events for best results.
- **Comparison**: Multiple snapshots over time help identify memory leaks vs. normal memory growth.
- **Node.js version**: Ensure your analysis tools support the Node.js version used by the application.
- **Collection methods**: The tool tries multiple approaches (kubectl debug, inspector, signals) but success depends on cluster permissions and application setup.
- **Troubleshooting failures**: If collection fails, check `heap-dump.log` and `collection-failed.txt` for specific guidance on enabling heap dumps.
