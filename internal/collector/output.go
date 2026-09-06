package collector

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

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
