package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Platform struct{}

func (p *Platform) Name() string { return "platform" }

type platformInfo struct {
	Platform   string `json:"platform"`
	Underlying string `json:"underlying"`
	OCPVersion string `json:"ocpVersion"`
	K8sVersion string `json:"k8sVersion"`
}

func (p *Platform) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Platform data collection...")

	outDir := filepath.Join(cfg.BasePath, "platform")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating platform output directory: %w", err)
	}

	info, err := p.detect(ctx, cfg)
	if err != nil {
		return err
	}

	jsonData, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling platform info: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "platform.json"), append(jsonData, '\n'), 0o644); err != nil {
		return err
	}

	var txt strings.Builder
	txt.WriteString("Detected Platform Information\n")
	txt.WriteString("-----------------------------\n")
	fmt.Fprintf(&txt, "Platform   : %s\n", info.Platform)
	fmt.Fprintf(&txt, "Underlying : %s\n", info.Underlying)
	if info.OCPVersion != "" {
		fmt.Fprintf(&txt, "OCP Version: %s\n", info.OCPVersion)
	}
	if info.K8sVersion != "" {
		fmt.Fprintf(&txt, "K8s Version: %s\n", info.K8sVersion)
	}
	if err := os.WriteFile(filepath.Join(outDir, "platform.txt"), []byte(txt.String()), 0o644); err != nil {
		return err
	}

	versionSuffix := ""
	if info.OCPVersion != "" {
		versionSuffix += fmt.Sprintf(" (OCP %s)", info.OCPVersion)
	}
	if info.K8sVersion != "" {
		versionSuffix += fmt.Sprintf(", K8s %s", info.K8sVersion)
	}
	log.Info("Detected %s on %s%s", info.Platform, info.Underlying, versionSuffix)
	log.Success("Platform data collection completed.")
	return nil
}

func (p *Platform) detect(ctx context.Context, cfg *Config) (*platformInfo, error) {
	info := &platformInfo{}

	isOCP, err := cfg.Client.HasAPIGroup("config.openshift.io")
	if err != nil {
		return nil, fmt.Errorf("checking API groups: %w", err)
	}

	if isOCP {
		p.detectOCP(ctx, cfg, info)
	} else {
		p.detectK8s(ctx, cfg, info)
	}

	return info, nil
}

var clusterVersionGVR = schema.GroupVersionResource{
	Group:    "config.openshift.io",
	Version:  "v1",
	Resource: "clusterversions",
}

var infrastructureGVR = schema.GroupVersionResource{
	Group:    "config.openshift.io",
	Version:  "v1",
	Resource: "infrastructures",
}

func (p *Platform) detectOCP(ctx context.Context, cfg *Config, info *platformInfo) {
	info.Platform = "OCP"

	cv, err := cfg.Client.Dynamic.Resource(clusterVersionGVR).Get(ctx, "version", metav1.GetOptions{})
	if err == nil {
		if desired, ok, _ := nestedString(cv.Object, "status", "desired", "version"); ok {
			info.OCPVersion = desired
		}
		if k8sVer, ok, _ := nestedString(cv.Object, "status", "desired", "kubernetesVersion"); ok {
			info.K8sVersion = k8sVer
		}
	}

	if info.K8sVersion == "" {
		info.K8sVersion = p.getServerVersion(cfg)
	}

	infra, err := cfg.Client.Dynamic.Resource(infrastructureGVR).Get(ctx, "cluster", metav1.GetOptions{})
	if err == nil {
		if pt, ok, _ := nestedString(infra.Object, "status", "platformStatus", "type"); ok {
			info.Underlying = pt
		} else if pt, ok, _ := nestedString(infra.Object, "status", "platform"); ok {
			info.Underlying = pt
		}

		infraJSON, _ := json.Marshal(infra.Object)
		infraStr := string(infraJSON)

		switch info.Underlying {
		case "AWS":
			if strings.Contains(strings.ToLower(infraStr), "rosa.openshift.io") {
				info.Platform = "ROSA"
			}
		case "Azure":
			if strings.Contains(strings.ToLower(infraStr), "aro.openshift.io") {
				info.Platform = "ARO"
			}
		case "IBMCloud":
			info.Platform = "ROKS"
		}
	}
}

func (p *Platform) detectK8s(ctx context.Context, cfg *Config, info *platformInfo) {
	info.K8sVersion = p.getServerVersion(cfg)

	nodes, err := cfg.Client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		info.Platform = "Vanilla K8s"
		return
	}

	node := nodes.Items[0]
	providerID := node.Spec.ProviderID
	prefix := ""
	if i := strings.Index(providerID, ":"); i >= 0 {
		prefix = providerID[:i]
	}

	switch prefix {
	case "aws":
		info.Underlying = "AWS"
	case "gce":
		info.Underlying = "GCP"
	case "azure":
		info.Underlying = "Azure"
	case "ibm":
		info.Underlying = "IBMCloud"
	case "vsphere":
		info.Underlying = "vSphere"
	default:
		info.Underlying = prefix
	}

	allNodes, err := cfg.Client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		info.Platform = "Vanilla K8s"
		return
	}

	info.Platform = "Vanilla K8s"
	for _, n := range allNodes.Items {
		labels := n.Labels
		switch {
		case info.Underlying == "AWS" && labels["eks.amazonaws.com/nodegroup"] != "":
			info.Platform = "EKS"
			return
		case info.Underlying == "GCP" && labels["cloud.google.com/gke-nodepool"] != "":
			info.Platform = "GKE"
			return
		case info.Underlying == "Azure" && labels["agentpool"] != "":
			info.Platform = "AKS"
			return
		}
	}
}

func (p *Platform) getServerVersion(cfg *Config) string {
	sv, err := cfg.Client.Discovery.ServerVersion()
	if err != nil {
		return ""
	}
	return sv.GitVersion
}

func nestedString(obj map[string]any, fields ...string) (string, bool, error) {
	current := obj
	for i, field := range fields {
		val, ok := current[field]
		if !ok {
			return "", false, nil
		}
		if i == len(fields)-1 {
			s, ok := val.(string)
			return s, ok, nil
		}
		next, ok := val.(map[string]any)
		if !ok {
			return "", false, nil
		}
		current = next
	}
	return "", false, nil
}
