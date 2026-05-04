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

### Pre-commit Hooks

This project uses [pre-commit](https://pre-commit.com/) to enforce consistency between the Makefile, `.rhdh/docker/requirements.in`, and `.rhdh/docker/requirements.txt`.

#### Setup

```bash
pip install pre-commit
pre-commit install
```

Once installed, hooks run automatically on `git commit` for changed files. You can also run them manually:

```bash
pre-commit run --all-files
```

#### What the hooks check

| Hook | What it does |
|------|-------------|
| `check-requirements-version` | Ensures the `yq` version in `.rhdh/docker/requirements.in` matches `YQ_VERSION` in the Makefile. Auto-fixes on failure. |
| `pip-compile` | Regenerates `.rhdh/docker/requirements.txt` from `requirements.in` with pinned hashes (for hermetic builds). Auto-fixes on failure. |

Both hooks auto-fix files when they detect drift. If a hook modifies a file, it will fail the first time. Stage the changes and re-run:

```bash
pre-commit run --all-files   # fails, auto-fixes files
git add -u
pre-commit run --all-files   # should pass now
```

A CI workflow enforces these checks on every pull request. If the check fails, a bot comment will be posted on the PR with instructions.

### Vendored Dependencies

The source for [websocat](https://github.com/vi/websocat) is vendored as a [Git subtree](https://www.atlassian.com/git/tutorials/git-subtree) under `vendor/`. It is built from source in the Containerfile using a multi-stage build, since it is not available as an RPM package and pre-built binary downloads are not compatible with hermetic build requirements downstream.

[yq](https://github.com/kislyuk/yq) (a jq wrapper for YAML) is installed via `pip` in the Containerfile.

A weekly GitHub Actions workflow ([vendor-update.yaml](.github/workflows/vendor-update.yaml)) checks for new releases and automatically opens a PR to update the subtree.

To manually sync the vendored dependency to the version declared in the Makefile:

```bash
make vendor
```

To update to a specific new version:

```bash
make vendor-update VENDOR_NAME=websocat VENDOR_VERSION=v<NEW_VERSION>
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
