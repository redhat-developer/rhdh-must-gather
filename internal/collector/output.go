package collector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

// setGVK populates kind/apiVersion on typed client-go objects, which
// leave these fields empty after Get/List calls.
func setGVK(obj interface{ GetObjectKind() schema.ObjectKind }, kind, apiVersion string) {
	obj.GetObjectKind().SetGroupVersionKind(schema.FromAPIVersionAndKind(apiVersion, kind))
}

func writeResource(path string, obj any) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

var (
	resolvedKubectl string
	kubectlOnce     sync.Once
)

func kubectlCmd() string {
	kubectlOnce.Do(func() {
		for _, name := range []string{"oc", "kubectl"} {
			if p, err := exec.LookPath(name); err == nil {
				resolvedKubectl = p
				return
			}
		}
		resolvedKubectl = "kubectl"
	})
	return resolvedKubectl
}

// describeResource runs "kubectl describe" and writes the output.
// Extra args are appended after resourceType (e.g. a name or "-l selector").
func describeResource(ctx context.Context, path, resourceType, namespace string, args ...string) {
	cmdArgs := []string{"describe", resourceType}
	cmdArgs = append(cmdArgs, args...)
	if namespace != "" {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmd := exec.CommandContext(ctx, kubectlCmd(), cmdArgs...)
	out, err := cmd.CombinedOutput()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		content := fmt.Sprintf("kubectl describe %s failed: %v\n%s", resourceType, err, out)
		_ = os.WriteFile(path, []byte(content), 0o644)
		return
	}
	_ = os.WriteFile(path, out, 0o644)
}

func writeCollectError(path, description string, err error) {
	content := fmt.Sprintf("Command failed: %s\n\n=== Error Details ===\n%s\n", description, err)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
	log.Warn("\tFailed: %s — %v", description, err)
}
