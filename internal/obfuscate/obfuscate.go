// Package obfuscate runs openshift/must-gather-clean on a collected must-gather
// after the existing secret sanitizer. It obfuscates IP addresses, MAC addresses,
// and discovered cluster domain names. It does not omit ConfigMaps or Secrets.
package obfuscate

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mgclean "github.com/openshift/must-gather-clean/pkg/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

const (
	// EnvDomains is an optional comma-separated list of extra domain names to
	// obfuscate when cluster discovery misses a name the user needs removed.
	EnvDomains = "RHDH_OBFUSCATE_DOMAINS"

	reportFileName = "report.yaml"
	stagingDirName = ".obfuscated-staging"

	discoveryTimeout = 10 * time.Second
)

// CleanFunc runs must-gather-clean against input and writes the cleaned tree
// to output. The reversible replacement map is written under reportDir and
// must not be copied into the published gather.
type CleanFunc func(configPath, inputPath, outputPath, reportDir string) error

// Run discovers cluster domains and obfuscates basePath in place.
// On failure the original tree is left unchanged.
func Run(ctx context.Context, client *kube.Client, basePath string, clean CleanFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	discoverCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	return Apply(basePath, Discover(discoverCtx, client), clean)
}

// Apply obfuscates basePath using the supplied domain names.
// Domain names are not logged. report.yaml is written outside basePath and deleted.
func Apply(basePath string, domains []string, clean CleanFunc) error {
	if clean == nil {
		return fmt.Errorf("obfuscation cleaner is not configured")
	}
	info, err := os.Stat(basePath)
	if err != nil {
		return fmt.Errorf("obfuscation input: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("obfuscation input %s is not a directory", basePath)
	}

	work, err := os.MkdirTemp("", "rhdh-must-gather-obfuscate-")
	if err != nil {
		return fmt.Errorf("creating obfuscation work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	configPath := filepath.Join(work, "config.yaml")
	outputPath := filepath.Join(work, "output")
	reportDir := filepath.Join(work, "report")
	if err := os.Mkdir(reportDir, 0o700); err != nil {
		return fmt.Errorf("creating obfuscation report directory: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(configYAML(domains)), 0o600); err != nil {
		return fmt.Errorf("writing obfuscation config: %w", err)
	}

	if err := reportStaysOutside(outputPath, basePath, reportDir); err != nil {
		return err
	}
	if err := clean(configPath, basePath, outputPath, reportDir); err != nil {
		return err
	}
	reportPath := filepath.Join(reportDir, reportFileName)
	if _, err := os.Stat(reportPath); err != nil {
		return fmt.Errorf("obfuscation report was not written outside the gather: %w", err)
	}
	return publish(outputPath, basePath)
}

// Clean runs must-gather-clean in process. The gather command calls this from a
// subprocess so a klog.Exitf inside the library does not kill the collector.
func Clean(configPath, inputPath, outputPath, reportDir string, workers int) error {
	if workers < 1 {
		workers = 1
	}
	if err := mgclean.Run(configPath, inputPath, outputPath, false, reportDir, workers); err != nil {
		return fmt.Errorf("must-gather-clean: %w", err)
	}
	return nil
}

// Discover returns domain names that should be obfuscated.
// OpenShift base and ingress domains and the API server hostname are included
// when the API is reachable. In-cluster service names and IP addresses are
// dropped because IP obfuscation already covers addresses, and cluster.local
// names are not customer domains.
//
// When discovery finds nothing, IP and MAC obfuscation still run. Set
// EnvDomains to supply names that discovery cannot see.
func Discover(ctx context.Context, client *kube.Client) []string {
	var found []string
	if client != nil {
		if d, err := openShiftBaseDomain(ctx, client); err != nil {
			log.Warn("Could not read the OpenShift base domain: %v", err)
		} else if d != "" {
			found = append(found, d)
		}
		if d, err := openShiftIngressDomain(ctx, client); err != nil {
			log.Warn("Could not read the OpenShift ingress domain: %v", err)
		} else if d != "" {
			found = append(found, d)
		}
		if client.Config != nil {
			if h := APIServerHost(client.Config.Host); h != "" {
				found = append(found, h)
			}
		}
	}

	merged := mergeDomains(found, splitDomains(os.Getenv(EnvDomains)))
	if len(merged) == 0 {
		log.Warn("No cluster domains discovered. IP and MAC addresses will still be obfuscated. Set %s to a comma-separated list of domains to obfuscate.", EnvDomains)
		return nil
	}
	log.Info("Obfuscating %d domain name(s) plus IP and MAC addresses", len(merged))
	return merged
}

func openShiftBaseDomain(ctx context.Context, client *kube.Client) (string, error) {
	if client.Dynamic == nil {
		return "", nil
	}
	present, err := apiGroupPresent(client, "config.openshift.io")
	if err != nil || !present {
		return "", err
	}
	gvr := schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "dnses"}
	obj, err := client.Dynamic.Resource(gvr).Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	d, found, err := unstructured.NestedString(obj.Object, "spec", "baseDomain")
	if err != nil || !found {
		return "", err
	}
	return d, nil
}

func openShiftIngressDomain(ctx context.Context, client *kube.Client) (string, error) {
	if client.Dynamic == nil {
		return "", nil
	}
	present, err := apiGroupPresent(client, "operator.openshift.io")
	if err != nil || !present {
		return "", err
	}
	gvr := schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}
	obj, err := client.Dynamic.Resource(gvr).Namespace("openshift-ingress-operator").Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	d, found, err := unstructured.NestedString(obj.Object, "status", "domain")
	if err != nil || !found {
		return "", err
	}
	return d, nil
}

// apiGroupPresent reports whether group exists. A nil discovery client means
// the caller already knows the cluster and the lookup should be attempted.
func apiGroupPresent(client *kube.Client, group string) (bool, error) {
	if client.Discovery == nil {
		return true, nil
	}
	return client.HasAPIGroup(group)
}

// APIServerHost returns the hostname from a Kubernetes API server URL.
// IP addresses and empty values return an empty string.
func APIServerHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func splitDomains(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mergeDomains(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range groups {
		for _, d := range group {
			d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
			if !usableDomain(d) {
				continue
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}

func usableDomain(d string) bool {
	if d == "" || strings.ContainsAny(d, " /\\") || !strings.Contains(d, ".") {
		return false
	}
	if net.ParseIP(d) != nil {
		return false
	}
	switch d {
	case "localhost", "cluster.local", "svc.cluster.local",
		"kubernetes.default.svc", "kubernetes.default.svc.cluster.local":
		return false
	}
	if strings.HasSuffix(d, ".cluster.local") || strings.HasSuffix(d, ".svc") {
		return false
	}
	return true
}

// configYAML is the must-gather-clean config for RHDH gathers.
// It obfuscates only. It does not omit ConfigMaps or Secrets: those resources
// are the diagnostic payload, and secret values are already redacted.
func configYAML(domains []string) string {
	var b strings.Builder
	b.WriteString("config:\n")
	b.WriteString("  obfuscate:\n")
	b.WriteString("    - type: IP\n")
	b.WriteString("      replacementType: Consistent\n")
	b.WriteString("      target: All\n")
	b.WriteString("    - type: MAC\n")
	b.WriteString("      replacementType: Consistent\n")
	b.WriteString("      target: All\n")
	if len(domains) > 0 {
		b.WriteString("    - type: Domain\n")
		b.WriteString("      replacementType: Consistent\n")
		b.WriteString("      target: All\n")
		b.WriteString("      domainNames:\n")
		for _, d := range domains {
			b.WriteString("        - ")
			b.WriteString(strconv.Quote(d))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// reportStaysOutside rejects a report directory that would be copied into the
// published gather. Collected files named report.yaml are ordinary resources
// and are left in place. The reversible map is the report written under reportDir.
func reportStaysOutside(outputPath, basePath, reportDir string) error {
	if dirInside(outputPath, reportDir) || dirInside(basePath, reportDir) {
		return fmt.Errorf("obfuscation report directory must stay outside the published gather")
	}
	return nil
}

func dirInside(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func publish(cleaned, basePath string) error {
	staging := filepath.Join(basePath, stagingDirName)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("removing previous obfuscation staging directory: %w", err)
	}
	if err := os.Rename(cleaned, staging); err != nil {
		if copyErr := copyTree(cleaned, staging); copyErr != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("staging obfuscated output: %w", copyErr)
		}
	}

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return fmt.Errorf("reading collected output: %w", err)
	}
	for _, e := range entries {
		if e.Name() == stagingDirName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(basePath, e.Name())); err != nil {
			return fmt.Errorf("replacing collected output: %w", err)
		}
	}

	staged, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("reading staged obfuscated output: %w", err)
	}
	for _, e := range staged {
		from := filepath.Join(staging, e.Name())
		to := filepath.Join(basePath, e.Name())
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("publishing obfuscated file %s: %w", e.Name(), err)
		}
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("removing obfuscation staging directory: %w", err)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := info.Mode().Perm()
			if mode == 0 {
				mode = 0o755
			}
			return os.MkdirAll(target, mode)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
