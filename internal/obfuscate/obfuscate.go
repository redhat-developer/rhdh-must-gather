// Package obfuscate runs openshift/must-gather-clean on a collected must-gather
// after the existing secret sanitizer. It obfuscates IP addresses, MAC addresses,
// and discovered cluster domain names. It does not omit ConfigMaps or Secrets.
package obfuscate

import (
	"context"
	"fmt"
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

	discoveryTimeout = 10 * time.Second
)

// CleanFunc runs must-gather-clean against input and writes the cleaned tree
// to output. The reversible replacement map is written under reportDir and
// must not be copied into the published gather.
type CleanFunc func(configPath, inputPath, outputPath, reportDir string) error

// Run discovers cluster domains and obfuscates basePath in place.
// On failure the original tree is left unchanged.
func Run(ctx context.Context, client *kube.Client, basePath string, namespaces []string, clean CleanFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	discoverCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	return Apply(basePath, Discover(discoverCtx, client, namespaces), clean)
}

// Apply obfuscates basePath using the supplied domain names.
// Domain names are not logged. report.yaml is written inside a temporary work
// directory under basePath and removed when Apply returns.
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

	// Remove stale work directories left behind by a previous SIGKILL/OOM.
	cleanStaleWorkDirs(basePath)

	// Create the work directory inside basePath so it stays on the same
	// volume (e.g. the PVC in a must-gather pod). Creating it beside the
	// mount point (filepath.Dir) would land on the root filesystem, which
	// counts against the container's ephemeral-storage limit and causes
	// eviction when the gathered data is large (heap dumps, etc.).
	work, err := os.MkdirTemp(basePath, ".obfuscate-")
	if err != nil {
		return fmt.Errorf("creating obfuscation work directory: %w", err)
	}

	// Relocate collected data into work/input so clean's input tree does
	// not overlap with the output tree or the work directory itself.
	inputDir := filepath.Join(work, "input")
	if err := os.Mkdir(inputDir, 0o755); err != nil {
		return fmt.Errorf("creating obfuscation input directory: %w", err)
	}
	if err := relocate(basePath, inputDir, filepath.Base(work)); err != nil {
		return err
	}

	// Move omitted files (e.g. .heapsnapshot) out of the input tree before
	// calling must-gather-clean. The library's file omitter matches against
	// relative paths using filepath.Match, where * does not cross path
	// separators — so a flat glob like *.heapsnapshot never matches files
	// nested in subdirectories. Physically removing them is the only
	// reliable way to prevent the cleaner from loading them into memory.
	omittedDir := filepath.Join(work, "omitted")
	if err := os.Mkdir(omittedDir, 0o755); err != nil {
		restoreInput(inputDir, basePath)
		return fmt.Errorf("creating omitted files directory: %w", err)
	}
	if err := extractOmittedFiles(inputDir, omittedDir); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("extracting omitted files: %w", err)
	}

	configPath := filepath.Join(work, "config.yaml")
	outputPath := filepath.Join(work, "output")
	reportDir := filepath.Join(work, "report")
	if err := os.Mkdir(reportDir, 0o700); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("creating obfuscation report directory: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(configYAML(domains)), 0o600); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("writing obfuscation config: %w", err)
	}

	if err := clean(configPath, inputDir, outputPath, reportDir); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return err
	}
	reportPath := filepath.Join(reportDir, reportFileName)
	if _, err := os.Stat(reportPath); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("obfuscation report was not written: %w", err)
	}
	if err := restoreOmittedFiles(omittedDir, outputPath); err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("restoring omitted files: %w", err)
	}

	// Remove sensitive files before publication so they cannot leak into
	// the archive if a later step fails and the work dir stays behind.
	_ = os.RemoveAll(reportDir)
	_ = os.Remove(configPath)

	// Move obfuscated files from outputPath to basePath. Everything is on
	// the same filesystem so renames are atomic pointer swaps.
	outEntries, err := os.ReadDir(outputPath)
	if err != nil {
		recoverAll(omittedDir, inputDir, basePath)
		return fmt.Errorf("reading obfuscated output: %w", err)
	}
	for _, e := range outEntries {
		src := filepath.Join(outputPath, e.Name())
		dst := filepath.Join(basePath, e.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("publishing obfuscated file %s: %w", e.Name(), err)
		}
	}

	if err := os.RemoveAll(work); err != nil {
		return fmt.Errorf("removing obfuscation work directory: %w", err)
	}
	return nil
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
// On OpenShift it uses the cluster base domain and the default ingress
// controller domain. On Kubernetes, and when those OpenShift names cannot be
// read, it uses host names from Ingress resources and Routes in namespaces.
// The API server hostname is included on every cluster. In-cluster service
// names and IP addresses are dropped because IP obfuscation already covers
// addresses, and cluster.local names are not customer domains.
//
// When discovery finds nothing, IP and MAC obfuscation still run. Set
// EnvDomains to supply names that discovery cannot see.
func Discover(ctx context.Context, client *kube.Client, namespaces []string) []string {
	var found []string
	if client != nil {
		found = append(found, openShiftDomains(ctx, client)...)
		if len(found) == 0 {
			found = append(found, workloadHosts(ctx, client, namespaces)...)
		}
		if client.Config != nil {
			if h := apiServerHost(client.Config.Host); h != "" {
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

type clusterField struct {
	label     string
	gvr       schema.GroupVersionResource
	namespace string
	name      string
	path      []string
}

func openShiftDomains(ctx context.Context, client *kube.Client) []string {
	fields := []clusterField{
		{
			label: "OpenShift base domain",
			gvr:   schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "dnses"},
			name:  "cluster",
			path:  []string{"spec", "baseDomain"},
		},
		{
			label:     "OpenShift ingress domain",
			gvr:       schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"},
			namespace: "openshift-ingress-operator",
			name:      "default",
			path:      []string{"status", "domain"},
		},
	}
	var found []string
	for _, field := range fields {
		d, err := readClusterField(ctx, client, field)
		if err != nil {
			log.Warn("Could not read the %s: %v", field.label, err)
			continue
		}
		if d != "" {
			found = append(found, d)
		}
	}
	return found
}

func readClusterField(ctx context.Context, client *kube.Client, field clusterField) (string, error) {
	if client.Dynamic == nil {
		return "", nil
	}
	present, err := apiGroupPresent(client, field.gvr.Group)
	if err != nil || !present {
		return "", err
	}
	resource := client.Dynamic.Resource(field.gvr)
	var obj *unstructured.Unstructured
	if field.namespace == "" {
		obj, err = resource.Get(ctx, field.name, metav1.GetOptions{})
	} else {
		obj, err = resource.Namespace(field.namespace).Get(ctx, field.name, metav1.GetOptions{})
	}
	if err != nil {
		return "", err
	}
	d, found, err := unstructured.NestedString(obj.Object, field.path...)
	if err != nil || !found {
		return "", err
	}
	return d, nil
}

// workloadHosts reads Ingress and Route hosts from the namespaces being
// collected. An empty namespace list reads every namespace the client can see.
// Host names are not logged.
func workloadHosts(ctx context.Context, client *kube.Client, namespaces []string) []string {
	var hosts []string
	if client.Dynamic == nil {
		return nil
	}
	ingressGVR := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	for _, obj := range listNamespaced(ctx, client, ingressGVR, namespaces, "Ingress") {
		hosts = append(hosts, ingressHosts(obj)...)
	}
	present, err := apiGroupPresent(client, "route.openshift.io")
	if err != nil || !present {
		return hosts
	}
	routeGVR := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	for _, obj := range listNamespaced(ctx, client, routeGVR, namespaces, "Route") {
		if host, ok, _ := unstructured.NestedString(obj.Object, "spec", "host"); ok {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func ingressHosts(obj unstructured.Unstructured) []string {
	var hosts []string
	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	for _, rule := range rules {
		if host, ok, _ := unstructured.NestedString(asMap(rule), "host"); ok {
			hosts = append(hosts, host)
		}
	}
	tlsList, _, _ := unstructured.NestedSlice(obj.Object, "spec", "tls")
	for _, tls := range tlsList {
		more, _, _ := unstructured.NestedStringSlice(asMap(tls), "hosts")
		hosts = append(hosts, more...)
	}
	return hosts
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func listNamespaced(ctx context.Context, client *kube.Client, gvr schema.GroupVersionResource, namespaces []string, kind string) []unstructured.Unstructured {
	scopes := namespaces
	if len(scopes) == 0 {
		scopes = []string{metav1.NamespaceAll}
	}
	var all []unstructured.Unstructured
	for _, ns := range scopes {
		list, err := client.Dynamic.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			if ns == metav1.NamespaceAll {
				log.Warn("Could not list %s hosts: %v", kind, err)
			} else {
				log.Warn("Could not list %s hosts in namespace %s: %v", kind, ns, err)
			}
			continue
		}
		all = append(all, list.Items...)
	}
	return all
}

// apiGroupPresent reports whether group exists. A nil discovery client means
// the caller already knows the cluster and the lookup should be attempted.
func apiGroupPresent(client *kube.Client, group string) (bool, error) {
	if client.Discovery == nil {
		return true, nil
	}
	return client.HasAPIGroup(group)
}

// apiServerHost returns the hostname from a Kubernetes API server URL.
// IP addresses and empty values return an empty string.
func apiServerHost(raw string) string {
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

// omitPatterns lists filepath.Match globs for files that must-gather-clean
// should skip. These files are copied verbatim into the output by
// copyOmittedFiles after the cleaner runs.
var omitPatterns = []string{
	"*.heapsnapshot",
}

// configYAML is the must-gather-clean config for RHDH gathers.
// It obfuscates only. It does not omit ConfigMaps or Secrets: those resources
// are the diagnostic payload, and secret values are already redacted.
// Heap snapshot files are omitted because they are large binary blobs that
// would cause must-gather-clean to OOM or time out.
func configYAML(domains []string) string {
	var b strings.Builder
	b.WriteString("config:\n")
	b.WriteString("  omit:\n")
	for _, p := range omitPatterns {
		fmt.Fprintf(&b, "    - type: File\n      pattern: %q\n", p)
	}
	b.WriteString("  obfuscate:\n")
	writeObfuscation(&b, "IP")
	writeObfuscation(&b, "MAC")
	if len(domains) == 0 {
		return b.String()
	}
	writeObfuscation(&b, "Domain")
	b.WriteString("      domainNames:\n")
	for _, d := range domains {
		b.WriteString("        - ")
		b.WriteString(strconv.Quote(d))
		b.WriteString("\n")
	}
	return b.String()
}

func writeObfuscation(b *strings.Builder, kind string) {
	fmt.Fprintf(b, "    - type: %s\n      replacementType: Consistent\n      target: All\n", kind)
}

// relocate moves every entry in srcDir into dstDir except for skip.
// Both directories must be on the same filesystem so os.Rename is a fast
// pointer swap without copying data.
func relocate(srcDir, dstDir, skip string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("reading directory for relocation: %w", err)
	}
	for _, e := range entries {
		if e.Name() == skip {
			continue
		}
		if err := os.Rename(filepath.Join(srcDir, e.Name()), filepath.Join(dstDir, e.Name())); err != nil {
			return fmt.Errorf("relocating %s for obfuscation: %w", e.Name(), err)
		}
	}
	return nil
}

func cleanStaleWorkDirs(basePath string) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".obfuscate-") || !e.IsDir() {
			continue
		}
		stale := filepath.Join(basePath, e.Name())
		// Recover files that were relocated into this work directory by
		// a run that was killed before it could restore them. Files may
		// be in omitted/ (pre-clean), input/ (relocated), or output/
		// (post-clean, if restoreOmittedFiles partially ran).
		// Only recover omitted-pattern files from output/ — cleaner-
		// generated files may be incomplete and would overwrite the
		// originals already restored from input/.
		omitted := filepath.Join(stale, "omitted")
		input := filepath.Join(stale, "input")
		output := filepath.Join(stale, "output")
		recoverAll(omitted, input, basePath)
		recoverOmittedOnly(output, basePath)
		_ = os.RemoveAll(stale)
	}
}

// recoverAll restores omitted files back into inputDir, then moves
// inputDir contents back to basePath. Used on failure paths so the
// original collected tree is recovered.
func recoverAll(omittedDir, inputDir, basePath string) {
	restoreOmittedToInput(omittedDir, inputDir)
	restoreInput(inputDir, basePath)
}

// restoreOmittedToInput moves extracted files from omittedDir back into
// inputDir so they are included when restoreInput recovers the tree.
func restoreOmittedToInput(omittedDir, inputDir string) {
	_ = filepath.WalkDir(omittedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(omittedDir, path)
		if err != nil {
			return nil
		}
		dst := filepath.Join(inputDir, rel)
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.Rename(path, dst)
		return nil
	})
}

// recoverOmittedOnly moves only omit-pattern files from srcDir to dstDir.
// Used during stale cleanup to recover omitted files from the output tree
// without overwriting originals with potentially incomplete cleaner output.
func recoverOmittedOnly(srcDir, dstDir string) {
	_ = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		for _, pattern := range omitPatterns {
			matched, _ := filepath.Match(pattern, d.Name())
			if !matched {
				continue
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return nil
			}
			dst := filepath.Join(dstDir, rel)
			if _, err := os.Stat(dst); err == nil {
				return nil
			}
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			_ = os.Rename(path, dst)
			return nil
		}
		return nil
	})
}

// restoreInput moves all entries from inputDir back to basePath so the
// original collected tree is recovered after an obfuscation failure.
func restoreInput(inputDir, basePath string) {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.Rename(filepath.Join(inputDir, e.Name()), filepath.Join(basePath, e.Name()))
	}
}

// extractOmittedFiles moves files matching omitPatterns from inputDir into
// omittedDir, preserving relative paths. This physically removes them from
// the input tree so must-gather-clean never sees them.
func extractOmittedFiles(inputDir, omittedDir string) error {
	var count int
	err := filepath.WalkDir(inputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		for _, pattern := range omitPatterns {
			matched, _ := filepath.Match(pattern, d.Name())
			if !matched {
				continue
			}
			rel, err := filepath.Rel(inputDir, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(omittedDir, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(path, dst); err != nil {
				return fmt.Errorf("extracting omitted file %s: %w", rel, err)
			}
			count++
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if count > 0 {
		log.Info("Excluded %d file(s) from obfuscation", count)
	}
	return nil
}

// restoreOmittedFiles moves previously extracted files from omittedDir into
// outputDir, preserving relative paths.
func restoreOmittedFiles(omittedDir, outputDir string) error {
	var count int
	err := filepath.WalkDir(omittedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(omittedDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(outputDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(path, dst); err != nil {
			return fmt.Errorf("restoring omitted file %s: %w", rel, err)
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count > 0 {
		log.Info("Restored %d file(s) excluded from obfuscation", count)
	}
	return nil
}
