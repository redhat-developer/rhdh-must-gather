## Contributing Guide

### Reporting issues

To report issues against this repository, please use JIRA: https://issues.redhat.com/browse/RHIDP with Component: **Support**.

To browse the existing issues, you can use this [Query](https://issues.redhat.com/issues/?jql=project%20%3D%20%22Red%20Hat%20Internal%20Developer%20Platform%22%20%20AND%20component%20%3D%20Support%20AND%20resolution%20%3D%20Unresolved%20ORDER%20BY%20status%2C%20priority%2C%20updated%20%20%20%20DESC).

Contributions are welcome!

### Local Development/Testing

#### Testing

```bash

# View all available targets
make help

# Run locally (requires oc, kubectl, jq, yq, and access to a cluster)
make run-local

# Test specific script locally. Examples:
make run-script SCRIPT=helm    # Test only gather_helm
make run-script SCRIPT=operator # Test only gather_operator

# Test with OpenShift using oc adm must-gather
make deploy-openshift

# Test on regular Kubernetes (non-OpenShift) by creating a Job in the cluster
make deploy-k8s

# Clean up test artifacts and images
make clean
```

### Vendored Dependencies

The sources for [yq](https://github.com/mikefarah/yq) and [websocat](https://github.com/vi/websocat) are vendored as [Git subtrees](https://www.atlassian.com/git/tutorials/git-subtree) under `vendor/`. They are built from source in the Containerfile using multi-stage builds, since neither tool is available as an RPM package and pre-built binary downloads are not compatible with hermetic build requirements downstream.

A weekly GitHub Actions workflow ([vendor-update.yaml](.github/workflows/vendor-update.yaml)) checks for new releases and automatically opens a PR to update each subtree.

To manually update a vendored dependency:

```bash
# Update yq
git subtree pull --prefix=vendor/yq https://github.com/mikefarah/yq.git v<NEW_VERSION> --squash

# Update websocat
git subtree pull --prefix=vendor/websocat https://github.com/vi/websocat.git v<NEW_VERSION> --squash
```

#### Building the Image

```bash
# Build locally
make image-build

# Build and push to registry
make image-push REGISTRY=your-registry.com IMAGE_NAME=namespace/rhdh-must-gather

# Build and push with custom image name and tag
make image-push REGISTRY=your-registry.com IMAGE_NAME=namespace/my-rhdh-must-gather IMAGE_TAG=v1.0.0
```
