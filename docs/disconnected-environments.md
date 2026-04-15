# Running in Disconnected/Airgapped Environments

This guide covers how to use RHDH Must-Gather in environments where the cluster cannot access the public internet.

## Overview

In disconnected environments, the cluster cannot reach external registries like `quay.io`. You must mirror the container image to an internal registry that the cluster can access. This guide covers two scenarios based on your network topology:

- **Partially disconnected**: Your local machine can access both the public internet and the internal mirror registry directly
- **Fully disconnected**: Your local machine can only access the public internet; a separate bastion host can access the internal mirror registry but not the public internet

## Prerequisites

- `skopeo` installed on your local machine (and on the bastion host for fully disconnected environments)
- An internal container registry accessible from your cluster
- (Kubernetes only) Access to the Helm chart, either from the public repository or a local copy

## Partially Disconnected Environments

In this scenario, your local machine has network access to both the public internet and your internal mirror registry. You can copy images directly.

### Mirror the Container Image

```bash
# Copy directly from quay.io to your internal registry
skopeo copy \
  docker://quay.io/rhdh-community/rhdh-must-gather:latest \
  docker://registry.example.com/rhdh/rhdh-must-gather:latest
```

**Pin to a specific version (recommended):**

```bash
# Using a version tag
skopeo copy \
  docker://quay.io/rhdh-community/rhdh-must-gather:v1.0.0 \
  docker://registry.example.com/rhdh/rhdh-must-gather:v1.0.0

# Using a digest for immutable references
skopeo copy \
  docker://quay.io/rhdh-community/rhdh-must-gather@sha256:<digest> \
  docker://registry.example.com/rhdh/rhdh-must-gather:v1.0.0
```

### Download the Helm Chart (Kubernetes only)

```bash
helm pull rhdh-must-gather --repo https://redhat-developer.github.io/rhdh-chart
```

### Run Must-Gather

See [Running with the Mirrored Image](#running-with-the-mirrored-image) below.

## Fully Disconnected Environments

In this scenario, your local machine can access the public internet but cannot reach the internal mirror registry. A bastion host can access the internal registry but cannot reach the public internet. You must transfer files between the two.

### Step 1: Mirror to Local Directory (on your local machine)

Save the container image to a local directory:

```bash
# Create a directory for the mirrored content
mkdir -p ./mirror/rhdh-must-gather

# Copy the image to a local directory
skopeo copy \
  docker://quay.io/rhdh-community/rhdh-must-gather:latest \
  dir:./mirror/rhdh-must-gather
```

**Pin to a specific version (recommended):**

```bash
skopeo copy \
  docker://quay.io/rhdh-community/rhdh-must-gather:v1.0.0 \
  dir:./mirror/rhdh-must-gather
```

Download the Helm chart (Kubernetes only):

```bash
helm pull rhdh-must-gather --repo https://redhat-developer.github.io/rhdh-chart --destination ./mirror/
```

### Step 2: Transfer Files to the Bastion Host

Transfer the `./mirror/` directory to your bastion host using your organization's approved file transfer method (e.g., `scp`, USB drive, secure file transfer):

```bash
# Example using scp
scp -r ./mirror/ user@bastion.example.com:/tmp/mirror/
```

### Step 3: Push to Internal Registry (on the bastion host)

Connect to the bastion host and push the image to your internal registry:

```bash
# Copy from the local directory to the internal registry
skopeo copy \
  dir:/tmp/mirror/rhdh-must-gather \
  docker://registry.example.com/rhdh/rhdh-must-gather:latest
```

### Step 4: Run Must-Gather

See [Running with the Mirrored Image](#running-with-the-mirrored-image) below.

## Running with the Mirrored Image

### OpenShift

```bash
oc adm must-gather --image=registry.example.com/rhdh/rhdh-must-gather:latest
```

### Kubernetes (Helm)

The Helm chart provides the following image configuration options:

| Parameter | Description |
|-----------|-------------|
| `image.registry` | Container registry (e.g., `registry.example.com`) |
| `image.repository` | Image repository path (e.g., `rhdh/rhdh-must-gather`) |
| `image.tag` | Image tag (e.g., `latest`, `v1.0.0`) |
| `image.digest` | Image digest for immutable references (overrides tag if set) |
| `imagePullSecrets` | List of pull secret names for registry authentication |

**From the remote chart repository (if accessible):**

```bash
helm install my-rhdh-must-gather rhdh-must-gather \
  --repo https://redhat-developer.github.io/rhdh-chart \
  --set image.registry=registry.example.com \
  --set image.repository=rhdh/rhdh-must-gather \
  --set image.tag=latest
```

**From a local chart file:**

```bash
helm install my-rhdh-must-gather ./rhdh-must-gather-*.tgz \
  --set image.registry=registry.example.com \
  --set image.repository=rhdh/rhdh-must-gather \
  --set image.tag=latest
```

**Using a digest for immutable references:**

```bash
helm install my-rhdh-must-gather ./rhdh-must-gather-*.tgz \
  --set image.registry=registry.example.com \
  --set image.repository=rhdh/rhdh-must-gather \
  --set image.digest=sha256:<digest>
```

## Image Pull Secrets

If your internal registry requires authentication, configure pull secrets.

### OpenShift

`oc adm must-gather` runs in a temporary namespace, so you must add your registry credentials to the cluster-wide pull secret:

```bash
# Get the existing pull secret
oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d > pull-secret.json

# Add your registry credentials (requires jq)
# Replace the values in <angle brackets>
jq --arg registry "registry.example.com" \
   --arg auth "$(echo -n '<username>:<password>' | base64)" \
   '.auths[$registry] = {"auth": $auth}' pull-secret.json > pull-secret-updated.json

# Update the cluster pull secret
oc set data secret/pull-secret -n openshift-config --from-file=.dockerconfigjson=pull-secret-updated.json

# Clean up
rm pull-secret.json pull-secret-updated.json
```

### Kubernetes

```bash
# Create the pull secret
kubectl create secret docker-registry my-registry-secret \
  --docker-server=registry.example.com \
  --docker-username=<username> \
  --docker-password=<password>

# Reference it in the Helm installation
helm install my-rhdh-must-gather ./rhdh-must-gather-*.tgz \
  --set image.registry=registry.example.com \
  --set image.repository=rhdh/rhdh-must-gather \
  --set image.tag=latest \
  --set imagePullSecrets[0].name=my-registry-secret
```

## Troubleshooting

### ImagePullBackOff errors

If pods fail to start with `ImagePullBackOff`:

1. Verify the image exists in your internal registry:
   ```bash
   skopeo inspect docker://registry.example.com/rhdh/rhdh-must-gather:latest
   ```

2. Check pull secret configuration:
   ```bash
   kubectl get secrets
   kubectl describe sa default
   ```

3. Verify the pod is using the correct image reference:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.containers[*].image}'
   ```

### Certificate errors

If your internal registry uses self-signed certificates:

- **OpenShift**: Add the CA to the cluster-wide trusted CA bundle via the `image.config.openshift.io/cluster` resource
- **Kubernetes**: Add the CA certificate to the container runtime's trusted certificates on each node, or configure the registry as insecure (not recommended for production)

### Verifying the mirrored image

Before running must-gather, verify the image was mirrored correctly:

```bash
# Check the image manifest
skopeo inspect docker://registry.example.com/rhdh/rhdh-must-gather:latest

# Compare digests between source and mirrored image
skopeo inspect docker://quay.io/rhdh-community/rhdh-must-gather:latest --format '{{.Digest}}'
skopeo inspect docker://registry.example.com/rhdh/rhdh-must-gather:latest --format '{{.Digest}}'
```
