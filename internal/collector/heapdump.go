package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

const (
	defaultHeapDumpTimeout = 600
	defaultHeapDumpMethod  = "inspector"
	backstageContainer     = "backstage-backend"
)

func collectHeapDumps(cfg *Config, ns, labelSelector, outDir, deployName, instanceName, kind string) {
	if !cfg.WithHeapDumps {
		return
	}
	if !matchesInstanceFilter(deployName, instanceName) {
		log.Debug("Skipping heap dump for %s (not in filter: %s)", deployName, os.Getenv("RHDH_HEAP_DUMP_INSTANCES"))
		return
	}

	log.Info("Collecting heap dumps for pods with labels: %s in namespace: %s", labelSelector, ns)
	heapDir := filepath.Join(outDir, "heap-dumps")
	_ = os.MkdirAll(heapDir, 0o755)

	timeout := heapDumpTimeout()
	method := heapDumpMethod()

	pods := findRunningPods(cfg, ns, labelSelector, kind)
	if len(pods) == 0 {
		log.Warn("No running pods found with labels: %s in namespace: %s", labelSelector, ns)
		_ = os.WriteFile(filepath.Join(heapDir, "no-pods.txt"), []byte("No running pods found\n"), 0o644)
		return
	}

	for _, pod := range pods {
		processHeapDumpPod(cfg, ns, pod, heapDir, timeout, method)
	}

	log.Info("Heap dump collection completed for namespace: %s", ns)
}

func matchesInstanceFilter(deployName, instanceName string) bool {
	filter := os.Getenv("RHDH_HEAP_DUMP_INSTANCES")
	if filter == "" {
		return true
	}
	for _, inst := range strings.Split(filter, ",") {
		inst = strings.TrimSpace(inst)
		if inst == "" {
			continue
		}
		if matchesInstance(deployName, inst) || matchesInstance(instanceName, inst) {
			return true
		}
	}
	return false
}

func matchesInstance(name, pattern string) bool {
	if name == "" || pattern == "" {
		return false
	}
	return name == pattern || strings.HasPrefix(name, pattern+"-") || strings.Contains(name, pattern)
}

func heapDumpTimeout() time.Duration {
	if s := os.Getenv("HEAP_DUMP_TIMEOUT"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return time.Duration(v) * time.Second
		}
	}
	return defaultHeapDumpTimeout * time.Second
}

func heapDumpMethod() string {
	if m := os.Getenv("RHDH_HEAP_DUMP_METHOD"); m != "" {
		return m
	}
	return defaultHeapDumpMethod
}

func findRunningPods(cfg *Config, ns, labelSelector, kind string) []string {
	client := cfg.Client.Clientset
	ctx := context.Background()

	podList, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: "status.phase=Running",
	})
	if err != nil || len(podList.Items) == 0 {
		return nil
	}

	ownerKind := ""
	switch WorkloadKind(kind) {
	case KindDeployment:
		ownerKind = "ReplicaSet"
	case KindStatefulSet:
		ownerKind = "StatefulSet"
	}

	var names []string
	for _, pod := range podList.Items {
		if ownerKind != "" && !hasOwnerKind(pod, ownerKind) {
			continue
		}
		names = append(names, pod.Name)
	}
	return names
}

func hasOwnerKind(pod corev1.Pod, kind string) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == kind {
			return true
		}
	}
	return false
}

func processHeapDumpPod(cfg *Config, ns, podName, heapDir string, timeout time.Duration, method string) {
	log.Info("Processing pod: %s for heap dump collection", podName)
	podDir := filepath.Join(heapDir, "pod="+podName)
	_ = os.MkdirAll(podDir, 0o755)

	ctx := context.Background()

	pod, err := cfg.Client.Clientset.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		log.Warn("Failed to get pod %s: %v", podName, err)
		return
	}
	writeResource(filepath.Join(podDir, "pod-spec.yaml"), pod)

	if !hasContainer(pod, backstageContainer) {
		log.Debug("Skipping pod %s (no backstage-backend container)", podName)
		return
	}

	warnLivenessProbeTimeout(pod, timeout)

	containerDir := filepath.Join(podDir, "container="+backstageContainer)
	_ = os.MkdirAll(containerDir, 0o755)

	logFile := filepath.Join(containerDir, "heap-dump.log")

	nodePID, err := findNodePID(ctx, cfg, ns, podName, backstageContainer)
	if err != nil {
		log.Warn("Failed to exec into container %s in pod %s: %v", backstageContainer, podName, err)
		_ = os.WriteFile(filepath.Join(containerDir, "no-node-process.txt"),
			[]byte(fmt.Sprintf("Failed to exec into container to find Node.js process\n\n=== Error Details ===\n%v\n", err)), 0o644)
		return
	}
	if nodePID == "" {
		log.Warn("No Node.js process found in backstage-backend container")
		_ = os.WriteFile(filepath.Join(containerDir, "no-node-process.txt"),
			[]byte("No Node.js process found in backstage-backend container\nSearched /proc filesystem for node process\nThis usually means the container is not running a Node.js application\n"), 0o644)
		return
	}

	log.Info("Found Node.js process (PID: %s) in backstage-backend container", nodePID)
	appendLog(logFile, "Node.js PID: %s\n", nodePID)

	collectProcessMetadata(ctx, cfg, ns, podName, backstageContainer, nodePID, containerDir)

	timestamp := time.Now().Format("20060102-150405")
	heapFile := fmt.Sprintf("heapdump-%s.heapsnapshot", timestamp)

	var collected bool
	switch method {
	case "inspector":
		collected = collectHeapDumpInspector(ctx, cfg, ns, podName, nodePID, containerDir, heapFile, logFile, timeout)
	case "sigusr2":
		collected = collectHeapDumpSIGUSR2(ctx, cfg, ns, podName, nodePID, containerDir, heapFile, logFile, timeout)
	default:
		log.Error("Unknown heap dump method: %s", method)
		appendLog(logFile, "Unknown heap dump method: %s\n", method)
	}

	if !collected {
		writeCollectionFailedGuidance(containerDir, method, nodePID, podName, backstageContainer, ns)
	}
}

func findNodePID(ctx context.Context, cfg *Config, ns, pod, container string) (string, error) {
	script := `
for pid_dir in /proc/[0-9]*; do
  pid=$(basename $pid_dir)
  if [ -f $pid_dir/comm ] && grep -qi node $pid_dir/comm 2>/dev/null; then
    echo $pid
    break
  fi
  if [ -f $pid_dir/cmdline ] && grep -qi node $pid_dir/cmdline 2>/dev/null; then
    echo $pid
    break
  fi
done
`
	out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, script)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func collectProcessMetadata(ctx context.Context, cfg *Config, ns, pod, container, pid, containerDir string) {
	script := fmt.Sprintf(`
echo "=== Process Information ==="
echo "PID: %s"
echo ""
echo "Process Status (/proc/%s/status):"
cat /proc/%s/status 2>/dev/null || echo "Could not read process status"
echo ""
echo "Command Line (/proc/%s/cmdline):"
cat /proc/%s/cmdline 2>/dev/null | tr '\0' ' ' || echo "Could not read command line"
echo ""
echo "Environment (/proc/%s/environ):"
cat /proc/%s/environ 2>/dev/null | tr '\0' '\n' | grep -E '^(NODE_|PATH=)' || echo "Could not read environment"
echo ""
echo "=== Memory Usage ==="
cat /proc/meminfo 2>/dev/null || echo "Could not get memory info"
echo ""
echo "=== Node.js Version ==="
node --version 2>/dev/null || echo "Could not get Node.js version"
echo ""
echo "=== Available Disk Space ==="
df -h 2>/dev/null || echo "Could not get disk space"
`, pid, pid, pid, pid, pid, pid, pid)

	out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, script)
	if err != nil {
		out = fmt.Sprintf("Failed to collect process metadata: %v\n", err)
	}
	_ = os.WriteFile(filepath.Join(containerDir, "process-info.txt"), []byte(out), 0o644)
}

func sendSignal(ctx context.Context, cfg *Config, ns, pod, container, pid, signal string) error {
	script := fmt.Sprintf(`kill -%s %s 2>/dev/null || node -e "process.kill(%s, 'SIG%s')" 2>/dev/null`, signal, pid, pid, signal)
	_, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, script)
	return err
}

func detectInspectorPort(ctx context.Context, cfg *Config, ns, pod, container, pid string) int {
	script := fmt.Sprintf(`
cmdline=$(cat /proc/%s/cmdline 2>/dev/null | tr '\0' ' ')
port=$(echo "$cmdline" | grep -oE '\-\-inspect(-brk)?=[^[:space:]]*' | head -1 | grep -oE '[0-9]+$')
if [ -n "$port" ]; then echo "$port"; exit 0; fi
env_opts=$(cat /proc/%s/environ 2>/dev/null | tr '\0' '\n' | grep '^NODE_OPTIONS=' | head -1)
port=$(echo "$env_opts" | grep -oE '\-\-inspect(-brk)?=[^[:space:]]*' | head -1 | grep -oE '[0-9]+$')
if [ -n "$port" ]; then echo "$port"; fi
`, pid, pid)

	out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, script)
	if err != nil {
		return 9229
	}
	out = strings.TrimSpace(out)
	if port, err := strconv.Atoi(out); err == nil && port > 0 {
		return port
	}
	return 9229
}

func isInspectorActive(ctx context.Context, cfg *Config, ns, pod, container string, port int) bool {
	portHex := fmt.Sprintf("%04X", port)
	script := fmt.Sprintf(`grep -qi ':%s' /proc/net/tcp 2>/dev/null || grep -qi ':%s' /proc/net/tcp6 2>/dev/null`, portHex, portHex)
	_, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, script)
	return err == nil
}

// collectHeapDumpInspector collects a heap dump via the Chrome DevTools Protocol.
// It replaces the bash+websocat implementation with pure Go using client-go port-forward
// and gorilla/websocket.
func collectHeapDumpInspector(ctx context.Context, cfg *Config, ns, pod, pid, containerDir, heapFile, logFile string, timeout time.Duration) bool {
	log.Info("Attempting heap dump collection via inspector protocol...")
	appendLog(logFile, "=== Inspector Protocol Heap Dump Collection ===\nPod: %s\nContainer: %s\nNode.js PID: %s\n\n", pod, backstageContainer, pid)

	inspectorPort := detectInspectorPort(ctx, cfg, ns, pod, backstageContainer, pid)

	if !isInspectorActive(ctx, cfg, ns, pod, backstageContainer, inspectorPort) {
		log.Info("Sending SIGUSR1 to activate inspector...")
		appendLog(logFile, "Sending SIGUSR1 to PID %s to activate inspector...\n", pid)
		if err := sendSignal(ctx, cfg, ns, pod, backstageContainer, pid, "USR1"); err != nil {
			appendLog(logFile, "Failed to send SIGUSR1 signal: %v\n", err)
			log.Warn("Failed to send SIGUSR1 to activate inspector")
			return false
		}
		time.Sleep(2 * time.Second)
	}

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	localPort, fw, err := startPortForward(cfg, ns, pod, inspectorPort, stopCh, readyCh, logFile)
	if err != nil {
		appendLog(logFile, "Failed to start port-forward: %v\n", err)
		log.Warn("Failed to start port-forward: %v", err)
		return false
	}
	defer close(stopCh)

	select {
	case <-readyCh:
	case <-time.After(10 * time.Second):
		appendLog(logFile, "Timeout waiting for port-forward to be ready\n")
		log.Warn("Port-forward not ready after 10 seconds")
		return false
	}
	_ = fw // keep reference

	appendLog(logFile, "Port-forward established: localhost:%d -> %s:%d\n", localPort, pod, inspectorPort)

	wsURL, err := getInspectorWSURL(localPort, inspectorPort)
	if err != nil {
		appendLog(logFile, "Failed to get WebSocket URL: %v\n", err)
		log.Warn("Failed to get inspector WebSocket URL: %v", err)
		return false
	}
	appendLog(logFile, "WebSocket URL: %s\n", wsURL)

	outPath := filepath.Join(containerDir, heapFile)
	if err := takeHeapSnapshot(wsURL, outPath, logFile, timeout); err != nil {
		appendLog(logFile, "Heap snapshot failed: %v\n\nAttempting fallback via v8.writeHeapSnapshot()...\n", err)
		log.Warn("Heap snapshot streaming failed: %v, trying fallback", err)

		close(stopCh)
		stopCh = make(chan struct{})
		readyCh = make(chan struct{})

		if err := sendSignal(ctx, cfg, ns, pod, backstageContainer, pid, "USR1"); err == nil {
			time.Sleep(2 * time.Second)
		}

		localPort2, fw2, err := startPortForward(cfg, ns, pod, inspectorPort, stopCh, readyCh, logFile)
		if err == nil {
			_ = fw2
			select {
			case <-readyCh:
				wsURL2, err := getInspectorWSURL(localPort2, inspectorPort)
				if err == nil {
					fallbackPath := strings.TrimSuffix(outPath, ".heapsnapshot") + ".fallback.heapsnapshot"
					if fallbackHeapDump(wsURL2, cfg, ns, pod, backstageContainer, fallbackPath, logFile, timeout) {
						close(stopCh)
						return true
					}
				}
			case <-time.After(10 * time.Second):
			}
			close(stopCh)
		}

		return false
	}

	fi, err := os.Stat(outPath)
	if err == nil {
		log.Info("Heap dump collected via inspector protocol (%s)", humanSize(fi.Size()))
		appendLog(logFile, "Heap snapshot saved: %s (%s)\n", outPath, humanSize(fi.Size()))
	}
	return true
}

func startPortForward(cfg *Config, ns, pod string, remotePort int, stopCh, readyCh chan struct{}, logFile string) (int, *portforward.PortForwarder, error) {
	localPort := remotePort + 10000 + os.Getpid()%1000

	transport, upgrader, err := spdy.RoundTripperFor(cfg.Client.Config)
	if err != nil {
		return 0, nil, fmt.Errorf("creating SPDY round tripper: %w", err)
	}

	req := cfg.Client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Name(pod).Namespace(ns).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, fmt.Errorf("creating port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	go func() {
		if err := <-errCh; err != nil {
			appendLog(logFile, "Port-forward error: %v\n", err)
		}
	}()

	return localPort, fw, nil
}

func getInspectorWSURL(localPort, inspectorPort int) (string, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/json", localPort))
	if err != nil {
		return "", fmt.Errorf("fetching inspector info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var targets []struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", fmt.Errorf("parsing inspector response: %w", err)
	}
	if len(targets) == 0 || targets[0].WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("no WebSocket URL in inspector response")
	}

	wsURL := targets[0].WebSocketDebuggerURL
	wsURL = strings.Replace(wsURL, fmt.Sprintf(":%d/", inspectorPort), fmt.Sprintf(":%d/", localPort), 1)
	return wsURL, nil
}

type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Message string `json:"message"`
}

type heapChunkParams struct {
	Chunk string `json:"chunk"`
}

type heapProgressParams struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

func takeHeapSnapshot(wsURL, outPath, logFile string, timeout time.Duration) error {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := conn.WriteJSON(map[string]any{"id": 1, "method": "HeapProfiler.enable"}); err != nil {
		return fmt.Errorf("sending HeapProfiler.enable: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	log.Info("Taking heap snapshot (this may take several minutes for large heaps)...")
	if err := conn.WriteJSON(map[string]any{
		"id": 2, "method": "HeapProfiler.takeHeapSnapshot",
		"params": map[string]any{"reportProgress": true},
	}); err != nil {
		return fmt.Errorf("sending takeHeapSnapshot: %w", err)
	}

	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)

	lastReportedPct := -1

	for {
		var msg cdpMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %s", timeout)
			}
			return fmt.Errorf("reading WebSocket: %w", err)
		}

		switch {
		case msg.Method == "HeapProfiler.addHeapSnapshotChunk":
			var p heapChunkParams
			if err := json.Unmarshal(msg.Params, &p); err == nil {
				_, _ = f.WriteString(p.Chunk)
			}

		case msg.Method == "HeapProfiler.reportHeapSnapshotProgress":
			var p heapProgressParams
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.Total > 0 {
				pct := p.Done * 100 / p.Total
				if pct/10 > lastReportedPct/10 {
					log.Info("Heap snapshot progress: %d%% (%d/%d)", pct, p.Done, p.Total)
					appendLog(logFile, "Progress: %d%% (%d/%d)\n", pct, p.Done, p.Total)
					lastReportedPct = pct
				}
			}

		case msg.ID == 2:
			if msg.Error != nil {
				return fmt.Errorf("HeapProfiler.takeHeapSnapshot failed: %s", msg.Error.Message)
			}
			appendLog(logFile, "HeapProfiler.takeHeapSnapshot completed successfully\n")
			return nil
		}
	}
}

func fallbackHeapDump(wsURL string, cfg *Config, ns, pod, container, outPath, logFile string, timeout time.Duration) bool {
	appendLog(logFile, "\n=== Fallback: v8.writeHeapSnapshot() ===\n")
	log.Info("Attempting fallback: writing heap dump directly to container filesystem...")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		appendLog(logFile, "Failed to connect WebSocket for fallback: %v\n", err)
		return false
	}
	defer func() { _ = conn.Close() }()

	remoteFile := os.Getenv("HEAP_DUMP_REMOTE_DIR")
	if remoteFile == "" {
		remoteFile = "/tmp"
	}
	remoteFile = fmt.Sprintf("%s/heapdump-fallback-%d.heapsnapshot", remoteFile, os.Getpid())

	writeCmd := fmt.Sprintf("require('v8').writeHeapSnapshot('%s')", remoteFile)
	if err := conn.WriteJSON(map[string]any{
		"id":     10,
		"method": "Runtime.evaluate",
		"params": map[string]any{"expression": writeCmd, "includeCommandLineAPI": true, "returnByValue": true},
	}); err != nil {
		appendLog(logFile, "Failed to send Runtime.evaluate: %v\n", err)
		return false
	}

	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	for {
		var msg cdpMessage
		if err := conn.ReadJSON(&msg); err != nil {
			appendLog(logFile, "Timeout waiting for v8.writeHeapSnapshot(): %v\n", err)
			return false
		}

		if msg.ID == 10 {
			if msg.Error != nil {
				appendLog(logFile, "v8.writeHeapSnapshot() failed: %s\n", msg.Error.Message)
				log.Warn("Fallback failed: %s", msg.Error.Message)
				return false
			}

			var result struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if err := json.Unmarshal(msg.Result, &result); err == nil && result.Result.Value != "" {
				remoteFile = result.Result.Value
			}

			appendLog(logFile, "Heap snapshot written to: %s\n", remoteFile)
			log.Info("Copying heap dump from container...")

			ctx := context.Background()
			copyScript := fmt.Sprintf("cat %q", remoteFile)
			data, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, copyScript)
			if err != nil {
				appendLog(logFile, "Failed to copy heap snapshot from container: %v\n", err)
				return false
			}

			if err := os.WriteFile(outPath, []byte(data), 0o644); err != nil {
				appendLog(logFile, "Failed to write fallback heap dump: %v\n", err)
				return false
			}

			cleanupScript := fmt.Sprintf("rm -f %q", remoteFile)
			_, _ = execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, container, cleanupScript)

			fi, _ := os.Stat(outPath)
			if fi != nil {
				log.Info("Fallback heap dump collected via v8.writeHeapSnapshot (%s)", humanSize(fi.Size()))
				appendLog(logFile, "Fallback heap snapshot copied: %s (%s)\n", outPath, humanSize(fi.Size()))
			}
			return true
		}
	}
}

func collectHeapDumpSIGUSR2(ctx context.Context, cfg *Config, ns, pod, pid, containerDir, heapFile, logFile string, timeout time.Duration) bool {
	log.Info("Sending SIGUSR2 signal to trigger heap dump...")
	appendLog(logFile, "Sending SIGUSR2 signal to Node.js process (PID: %s)...\n", pid)

	if err := sendSignal(ctx, cfg, ns, pod, backstageContainer, pid, "USR2"); err != nil {
		appendLog(logFile, "Failed to send SIGUSR2 signal: %v\n", err)
		log.Warn("Failed to send SIGUSR2 signal: %v", err)
		return false
	}
	appendLog(logFile, "SIGUSR2 sent successfully to PID %s\n", pid)

	remoteDir := os.Getenv("HEAP_DUMP_REMOTE_DIR")
	if remoteDir == "" {
		remoteDir = "/tmp"
	}
	searchPaths := remoteDir + " /tmp /app /opt/app-root/src"
	pollInterval := 5 * time.Second
	stableSeconds := 150
	if s := os.Getenv("HEAP_DUMP_SIGUSR2_STABLE_SECONDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			stableSeconds = v
		}
	}

	appendLog(logFile, "Polling for heap dump file (max %s, stable for %ds)...\n", timeout, stableSeconds)

	deadline := time.Now().Add(timeout)
	var foundFile string
	var lastSize int64
	stableCount := 0

	for time.Now().Before(deadline) {
		if foundFile == "" {
			script := fmt.Sprintf(`for p in %s; do f=$(find $p -maxdepth 2 -name '*.heapsnapshot' 2>/dev/null | head -1); [ -n "$f" ] && echo "$f" && break; done`, searchPaths)
			out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, backstageContainer, script)
			if err == nil {
				foundFile = strings.TrimSpace(out)
				if foundFile != "" {
					log.Info("Found heap dump file: %s (waiting for write to complete)", foundFile)
					appendLog(logFile, "Found heap dump file: %s\n", foundFile)
				}
			}
		}

		if foundFile != "" {
			sizeScript := fmt.Sprintf(`stat -c%%s %q 2>/dev/null || echo 0`, foundFile)
			out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, backstageContainer, sizeScript)
			if err == nil {
				currentSize, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
				if currentSize > 0 {
					if currentSize == lastSize {
						stableCount += int(pollInterval.Seconds())
						if stableCount >= stableSeconds {
							log.Info("Heap dump file size stable at %d bytes for %ds", currentSize, stableCount)
							appendLog(logFile, "File size stable at %d bytes for %ds - ready to copy\n", currentSize, stableCount)
							break
						}
					} else {
						stableCount = 0
						lastSize = currentSize
					}
				}
			}
		}

		time.Sleep(pollInterval)
	}

	if foundFile == "" || stableCount < stableSeconds {
		if foundFile != "" {
			appendLog(logFile, "Heap dump file found but not stable after %s\n", timeout)
			log.Warn("Heap dump file found but write did not complete within timeout")
		} else {
			appendLog(logFile, "No heap dump files found after %s in: %s\n", timeout, searchPaths)
		}
		return false
	}

	localPath := filepath.Join(containerDir, heapFile)
	copyScript := fmt.Sprintf("cat %q", foundFile)
	data, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, backstageContainer, copyScript)
	if err != nil {
		appendLog(logFile, "Failed to copy heap dump: %v\n", err)
		return false
	}

	if err := os.WriteFile(localPath, []byte(data), 0o644); err != nil {
		appendLog(logFile, "Failed to write heap dump: %v\n", err)
		return false
	}

	cleanupScript := fmt.Sprintf("rm -f %q", foundFile)
	_, _ = execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod, backstageContainer, cleanupScript)

	fi, _ := os.Stat(localPath)
	if fi != nil {
		log.Info("Heap dump copied (%s)", humanSize(fi.Size()))
		appendLog(logFile, "Heap dump collected: %s (%s)\n", heapFile, humanSize(fi.Size()))
	}
	return true
}

func warnLivenessProbeTimeout(pod *corev1.Pod, heapTimeout time.Duration) {
	for _, c := range pod.Spec.Containers {
		if c.Name != backstageContainer || c.LivenessProbe == nil {
			continue
		}
		ft := int32(3)
		ps := int32(10)
		if c.LivenessProbe.FailureThreshold > 0 {
			ft = c.LivenessProbe.FailureThreshold
		}
		if c.LivenessProbe.PeriodSeconds > 0 {
			ps = c.LivenessProbe.PeriodSeconds
		}
		probeTimeout := time.Duration(ft*ps) * time.Second
		if probeTimeout < heapTimeout {
			recommended := (int(heapTimeout.Seconds()) + int(ps) - 1) / int(ps)
			log.Warn("Pod '%s' may restart during heap dump collection!", pod.Name)
			log.Warn("  Current: failureThreshold=%d x periodSeconds=%ds = %s before restart", ft, ps, probeTimeout)
			log.Warn("  Required: at least %s (HEAP_DUMP_TIMEOUT)", heapTimeout)
			log.Warn("  To prevent pod restarts, set failureThreshold >= %d", recommended)
		}
	}
}

func writeCollectionFailedGuidance(containerDir, method, pid, pod, container, ns string) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "===================================================================\n")
	fmt.Fprintf(&sb, "Heap Dump Collection Failed\n")
	fmt.Fprintf(&sb, "===================================================================\n\n")
	fmt.Fprintf(&sb, "Method used: %s\n\n", method)
	fmt.Fprintf(&sb, "Node.js Process Information:\n")
	fmt.Fprintf(&sb, "  PID: %s\n", pid)
	fmt.Fprintf(&sb, "  Container: %s\n", container)
	fmt.Fprintf(&sb, "  Pod: %s\n", pod)
	fmt.Fprintf(&sb, "  Namespace: %s\n\n", ns)

	if method == "inspector" {
		sb.WriteString("===================================================================\n")
		sb.WriteString("Why Inspector Protocol Failed\n")
		sb.WriteString("===================================================================\n\n")
		sb.WriteString("Common reasons for failure:\n\n")
		sb.WriteString("  - NODE_OPTIONS contains --disable-sigusr1 (prevents inspector activation)\n")
		sb.WriteString("  - Security policies blocking the inspector port\n")
		sb.WriteString("  - Container doesn't have write access to /tmp\n\n")
		sb.WriteString("Check heap-dump.log for detailed error messages.\n\n")
		sb.WriteString("Alternatively, try the SIGUSR2 method:\n\n")
		sb.WriteString("  ./gather --with-heap-dumps --heap-dump-method sigusr2\n\n")
	} else {
		sb.WriteString("===================================================================\n")
		sb.WriteString("Why SIGUSR2 Method Failed\n")
		sb.WriteString("===================================================================\n\n")
		sb.WriteString("SIGUSR2 method requires NODE_OPTIONS configuration:\n\n")
		sb.WriteString("  env:\n")
		sb.WriteString("  - name: NODE_OPTIONS\n")
		sb.WriteString("    value: \"--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp\"\n\n")
		sb.WriteString("Alternatively, try the inspector method (default):\n\n")
		sb.WriteString("  ./gather --with-heap-dumps --heap-dump-method inspector\n\n")
	}

	sb.WriteString("===================================================================\n")
	sb.WriteString("Diagnostic Logs\n")
	sb.WriteString("===================================================================\n\n")
	sb.WriteString("For detailed logs: heap-dump.log\n")
	sb.WriteString("For process info: process-info.txt\n")

	_ = os.WriteFile(filepath.Join(containerDir, "collection-failed.txt"), []byte(sb.String()), 0o644)
	log.Info("Created guidance file: %s/collection-failed.txt", containerDir)
}

func appendLog(path, format string, args ...any) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, format, args...)
	_ = f.Close()
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%dMB", bytes>>20)
	case bytes >= 1<<10:
		return fmt.Sprintf("%dKB", bytes>>10)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
