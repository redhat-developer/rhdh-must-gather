package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

// CollectPodData collects app data from a running pod's backstage-backend container.
// Optimized: batches 8+ separate exec calls from the bash version into 2.
func CollectPodData(ctx context.Context, cfg *Config, ns string, pod *corev1.Pod, outDir string) {
	if !hasContainer(pod, "backstage-backend") {
		return
	}
	_ = os.MkdirAll(outDir, 0o755)

	// Batch 1: Collect identity, env vars, versions, and node version in a single exec.
	// The bash version does 4 separate execs for these.
	envScript := `
echo "===ID==="
id 2>/dev/null || echo "unknown"
echo "===ENV==="
env | grep -E "^(BACKSTAGE_|RHDH_|UPSTREAM_REPO|MIDSTREAM_REPO|NODE_|APP_CONFIG_|LOG_LEVEL|PLUGIN_|NO_PROXY|HTTP_PROXY|HTTPS_PROXY|NPM_CONFIG_|GLOBAL_AGENT_)" | sort || true
echo "===VERSIONS==="
echo "BACKSTAGE_VERSION=${BACKSTAGE_VERSION:-}"
echo "RHDH_VERSION=${RHDH_VERSION:-}"
echo "UPSTREAM_REPO=${UPSTREAM_REPO:-}"
echo "MIDSTREAM_REPO=${MIDSTREAM_REPO:-}"
echo "===NODE_VERSION==="
node --version 2>/dev/null || echo "unknown"
`
	log.Info("\tCollecting: app data from %s (batched)", pod.Name)
	envOut, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod.Name, "backstage-backend", envScript)
	if err != nil {
		log.Warn("Failed to collect app data from %s: %v", pod.Name, err)
		_ = os.WriteFile(filepath.Join(outDir, "collection-error.txt"),
			[]byte(fmt.Sprintf("Failed to exec into pod: %v\n", err)), 0o644)
		return
	}

	sections := parseSections(envOut)

	_ = os.WriteFile(filepath.Join(outDir, "app-container-userid.txt"), []byte(sections["ID"]+"\n"), 0o644)

	envVars := "=== RHDH/Backstage Environment Variables ===\n\n" + sections["ENV"] + "\n"
	_ = os.WriteFile(filepath.Join(outDir, "env-vars.txt"), []byte(envVars), 0o644)

	_ = os.WriteFile(filepath.Join(outDir, "node-version.txt"), []byte(strings.TrimSpace(sections["NODE_VERSION"])+"\n"), 0o644)

	versions := parseKeyValues(sections["VERSIONS"])
	writeBackstageJSON(ctx, cfg, ns, pod.Name, outDir, versions["BACKSTAGE_VERSION"])
	writeBuildMetadata(ctx, cfg, ns, pod.Name, outDir, versions)

	// Batch 2: Collect dynamic plugins listing and config in a single exec.
	// The bash version does 3 separate execs + a tar pipeline for these.
	pluginsScript := `
echo "===LS==="
ls -lhrta dynamic-plugins-root 2>/dev/null || echo "directory not found"
echo "===CONFIG==="
cat /opt/app-root/src/dynamic-plugins-root/app-config.dynamic-plugins.yaml 2>/dev/null || echo "file not found"
echo "===PACKAGES==="
find /opt/app-root/src/dynamic-plugins-root -maxdepth 2 -name package.json -exec sh -c 'echo "===FILE:{}==="; cat "{}"' \; 2>/dev/null || true
`
	pluginsOut, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod.Name, "backstage-backend", pluginsScript)
	if err != nil {
		log.Warn("Failed to collect dynamic plugins from %s: %v", pod.Name, err)
		return
	}

	pluginSections := parseSections(pluginsOut)
	_ = os.WriteFile(filepath.Join(outDir, "dynamic-plugins-root.fs.txt"), []byte(pluginSections["LS"]+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(outDir, "app-config.dynamic-plugins.yaml"), []byte(pluginSections["CONFIG"]+"\n"), 0o644)

	writePluginPackageJSON(outDir, pluginSections["PACKAGES"])
}

// CollectProcesses collects the process list from all containers in a pod.
// Optimized: uses a single compact exec instead of the 50+ line bash script.
func CollectProcesses(ctx context.Context, cfg *Config, ns string, pod *corev1.Pod, outDir string) {
	_ = os.MkdirAll(outDir, 0o755)

	allContainers := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	allContainers = append(allContainers, pod.Spec.InitContainers...)
	allContainers = append(allContainers, pod.Spec.Containers...)

	for _, c := range allContainers {
		script := fmt.Sprintf(`
echo "=== Process List (from /proc filesystem) ==="
echo "Container: %s"
echo "Pod: %s"
echo "Namespace: %s"
echo "Collected at: $(date -u +"%%Y-%%m-%%dT%%H:%%M:%%SZ" 2>/dev/null || date)"
echo ""
printf "%%-7s %%-7s %%-5s %%-10s %%-10s %%-20s %%s\n" "PID" "PPID" "STATE" "RSS(KB)" "VSZ(KB)" "NAME" "CMDLINE"
count=0
for d in /proc/[0-9]*; do
  p=$(basename "$d")
  [ "$p" = "$$" ] && continue
  [ ! -f "$d/status" ] && continue
  name=$(cat "$d/comm" 2>/dev/null || echo "")
  ppid=""; state=""; rss="-"; vsz="-"
  while IFS= read -r line; do
    case "$line" in
      PPid:*)  ppid=${line#*:}; ppid=$(echo $ppid) ;;
      State:*) state=${line#*:}; state=$(echo $state | cut -c1) ;;
      VmRSS:*) rss=${line#*:}; rss=$(echo $rss | awk '{print $1}') ;;
      VmSize:*) vsz=${line#*:}; vsz=$(echo $vsz | awk '{print $1}') ;;
    esac
  done < "$d/status"
  cmd=$(cat "$d/cmdline" 2>/dev/null | tr "\0" " " | head -c 200 || echo "")
  [ -z "$cmd" ] && [ -n "$name" ] && cmd="[$name]"
  printf "%%-7s %%-7s %%-5s %%-10s %%-10s %%-20s %%s\n" "$p" "$ppid" "$state" "$rss" "$vsz" "$name" "$cmd"
  count=$((count + 1))
done
echo ""
echo "Total processes: $count"
`, c.Name, pod.Name, ns)
		out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, pod.Name, c.Name, script)
		if err != nil {
			_ = os.WriteFile(filepath.Join(outDir, "container="+c.Name+".txt"),
				[]byte(fmt.Sprintf("Failed to collect processes: %v\n", err)), 0o644)
			continue
		}
		_ = os.WriteFile(filepath.Join(outDir, "container="+c.Name+".txt"), []byte(out), 0o644)
	}
}

// CollectPodLogs collects current and previous logs for all containers using client-go GetLogs API.
// Optimized: uses the streaming API directly instead of shelling out to kubectl.
func CollectPodLogs(ctx context.Context, cfg *Config, ns string, pod *corev1.Pod, outDir string) {
	_ = os.MkdirAll(outDir, 0o755)
	client := cfg.Client.Clientset

	allContainers := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	allContainers = append(allContainers, pod.Spec.InitContainers...)
	allContainers = append(allContainers, pod.Spec.Containers...)

	for _, c := range allContainers {
		cDir := filepath.Join(outDir, "container="+c.Name)
		_ = os.MkdirAll(cDir, 0o755)

		streamAndSaveLogs(ctx, client, ns, pod.Name, c.Name, false, filepath.Join(cDir, "current.txt"))
		streamAndSaveLogs(ctx, client, ns, pod.Name, c.Name, true, filepath.Join(cDir, "previous.txt"))
	}

	writeAggregatedLogs(ctx, client, ns, []corev1.Pod{*pod}, false, filepath.Join(outDir, "logs-app.current.txt"))
	writeAggregatedLogs(ctx, client, ns, []corev1.Pod{*pod}, true, filepath.Join(outDir, "logs-app.previous.txt"))
}

func streamAndSaveLogs(ctx context.Context, client kubernetes.Interface, ns, podName, container string, previous bool, outPath string) {
	opts := &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
	}
	req := client.CoreV1().Pods(ns).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		_ = os.WriteFile(outPath, []byte(fmt.Sprintf("Failed to get logs: %v\n", err)), 0o644)
		return
	}
	defer func() { _ = stream.Close() }()

	data, err := io.ReadAll(stream)
	if err != nil {
		_ = os.WriteFile(outPath, []byte(fmt.Sprintf("Failed to read logs: %v\n", err)), 0o644)
		return
	}
	_ = os.WriteFile(outPath, data, 0o644)
}

func execInPod(ctx context.Context, config *rest.Config, client kubernetes.Interface, ns, podName, container, script string) (string, error) {
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   []string{"sh", "-c", script},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("creating executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("exec failed: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

func parseSections(output string) map[string]string {
	sections := map[string]string{}
	var currentKey string
	var currentLines []string

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") {
			key := strings.Trim(line, "= ")
			// Only treat as a section boundary if the key looks like a
			// top-level marker (ID, ENV, PACKAGES, …). Nested markers
			// like ===FILE:/path=== contain ':' or '/' and must stay as
			// content within their parent section.
			if !strings.ContainsAny(key, ":/") {
				if currentKey != "" {
					sections[currentKey] = strings.TrimSpace(strings.Join(currentLines, "\n"))
				}
				currentKey = key
				currentLines = nil
				continue
			}
		}
		currentLines = append(currentLines, line)
	}
	if currentKey != "" {
		sections[currentKey] = strings.TrimSpace(strings.Join(currentLines, "\n"))
	}
	return sections
}

func parseKeyValues(block string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return kv
}

func writeBackstageJSON(ctx context.Context, cfg *Config, ns, podName, outDir, backstageVersion string) {
	outPath := filepath.Join(outDir, "backstage.json")
	if backstageVersion != "" {
		data := map[string]string{"version": backstageVersion, "source": "BACKSTAGE_VERSION env var"}
		jsonData, _ := json.MarshalIndent(data, "", "  ")
		_ = os.WriteFile(outPath, append(jsonData, '\n'), 0o644)
		return
	}

	// Fallback: try to read backstage.json from the container
	log.Debug("BACKSTAGE_VERSION not set in %s, trying file fallback", podName)
	out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, podName, "backstage-backend",
		"cat /opt/app-root/src/backstage.json 2>/dev/null || echo '{}'")
	if err == nil {
		_ = os.WriteFile(outPath, []byte(out), 0o644)
	}
}

func writeBuildMetadata(ctx context.Context, cfg *Config, ns, podName, outDir string, versions map[string]string) {
	outPath := filepath.Join(outDir, "build-metadata.json")
	rhdh := versions["RHDH_VERSION"]
	upstream := versions["UPSTREAM_REPO"]
	midstream := versions["MIDSTREAM_REPO"]

	if rhdh != "" || upstream != "" || midstream != "" {
		data := map[string]string{
			"rhdh_version":  rhdh,
			"upstream_repo": upstream,
			"midstream_repo": midstream,
			"source":        "environment variables",
		}
		jsonData, _ := json.MarshalIndent(data, "", "  ")
		_ = os.WriteFile(outPath, append(jsonData, '\n'), 0o644)
		return
	}

	log.Debug("Build metadata env vars not set in %s, trying file fallback", podName)
	out, err := execInPod(ctx, cfg.Client.Config, cfg.Client.Clientset, ns, podName, "backstage-backend",
		"cat /opt/app-root/src/packages/app/src/build-metadata.json 2>/dev/null || echo '{}'")
	if err == nil {
		_ = os.WriteFile(outPath, []byte(out), 0o644)
	}
}

func writePluginPackageJSON(outDir, packagesOutput string) {
	pluginsDir := filepath.Join(outDir, "dynamic-plugins-root")
	_ = os.MkdirAll(pluginsDir, 0o755)

	if packagesOutput == "" {
		return
	}

	var currentPath string
	var currentContent []string
	for _, line := range strings.Split(packagesOutput, "\n") {
		if strings.HasPrefix(line, "===FILE:") && strings.HasSuffix(line, "===") {
			if currentPath != "" {
				writePluginFile(pluginsDir, currentPath, strings.Join(currentContent, "\n"))
			}
			currentPath = strings.TrimPrefix(strings.TrimSuffix(line, "==="), "===FILE:")
			currentContent = nil
		} else {
			currentContent = append(currentContent, line)
		}
	}
	if currentPath != "" {
		writePluginFile(pluginsDir, currentPath, strings.Join(currentContent, "\n"))
	}
}

func writePluginFile(baseDir, remotePath, content string) {
	// remotePath is like /opt/app-root/src/dynamic-plugins-root/plugin-name/package.json
	// We want to preserve just the relative part under dynamic-plugins-root
	const prefix = "/opt/app-root/src/dynamic-plugins-root/"
	rel := strings.TrimPrefix(remotePath, prefix)
	if rel == remotePath {
		rel = filepath.Base(remotePath)
	}
	outPath := filepath.Join(baseDir, rel)
	_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
	_ = os.WriteFile(outPath, []byte(content), 0o644)
}

func hasContainer(pod *corev1.Pod, name string) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}
