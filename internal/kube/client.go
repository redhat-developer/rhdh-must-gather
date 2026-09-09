package kube

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
	Config    *rest.Config
}

func NewClient() (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("creating kubernetes config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &Client{
		Clientset: clientset,
		Dynamic:   dynClient,
		Discovery: clientset.Discovery(),
		Config:    config,
	}, nil
}

func (c *Client) HasAPIGroup(group string) (bool, error) {
	groups, err := c.Discovery.ServerGroups()
	if err != nil {
		return false, fmt.Errorf("listing API groups: %w", err)
	}
	for _, g := range groups.Groups {
		if g.Name == group {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) PreferredVersion(group string) (string, error) {
	groups, err := c.Discovery.ServerGroups()
	if err != nil {
		return "", fmt.Errorf("listing API groups: %w", err)
	}
	for _, g := range groups.Groups {
		if g.Name == group {
			return g.PreferredVersion.Version, nil
		}
	}
	return "", fmt.Errorf("API group %s not found", group)
}
