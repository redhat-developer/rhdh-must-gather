package kube

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Client struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
	Config    *rest.Config
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
