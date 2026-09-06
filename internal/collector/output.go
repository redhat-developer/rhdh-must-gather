package collector

import (
	"fmt"
	"os"
	"path/filepath"

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

func writeCollectError(path, description string, err error) {
	content := fmt.Sprintf("Command failed: %s\n\n=== Error Details ===\n%s\n", description, err)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
	log.Warn("\tFailed: %s — %v", description, err)
}
