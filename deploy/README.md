# Kustomize Deployment for RHDH Must-Gather

This directory contains Kustomize configurations for deploying the RHDH must-gather tool on standard Kubernetes clusters.

## Architecture

The tool runs as a single **Deployment** with:
- An **init container** (`gather`) that runs the must-gather collection scripts
- A **main container** (`data-holder`) that sleeps indefinitely, allowing users to retrieve the collected data via `kubectl exec`
- An **ephemeral volume** (inline `volumeClaimTemplate`) that is automatically provisioned and cleaned up with the pod lifecycle

## Directory Structure

```
deploy/
├── kustomization.yaml          # Default kustomization (references base)
├── base/                       # Base configuration (all required resources)
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── serviceaccount.yaml
│   ├── rbac.yaml
│   └── deployment.yaml
└── overlays/                   # Example customization overlays
    ├── custom-namespace/       # Deploy to a custom namespace
    ├── debug-mode/             # Enable debug logging with increased resources
    ├── with-heap-dumps/        # Enable heap dump collection
    ├── specific-namespaces/    # Collect from specific namespaces only
    └── custom-image/           # Use a custom image or tag
```

## Quick Start

### Basic Deployment

```bash
# Deploy using the default configuration
kubectl apply -k deploy/

# Or directly from GitHub
kubectl apply -k https://github.com/redhat-developer/rhdh-must-gather/deploy?ref=main
```

### Using Pre-built Overlays

```bash
# Deploy with debug logging enabled
kubectl apply -k deploy/overlays/debug-mode/

# Deploy with heap dump collection
kubectl apply -k deploy/overlays/with-heap-dumps/
```

## Available Overlays

| Overlay | Description | Key Changes |
|---------|-------------|-------------|
| `custom-namespace` | Deploy to a different namespace | Changes namespace from `rhdh-must-gather` |
| `debug-mode` | Enable verbose logging | Sets `LOG_LEVEL=DEBUG`, increases memory limits |
| `with-heap-dumps` | Collect heap dumps | Adds `--with-heap-dumps` arg, 10Gi storage, increased memory |
| `specific-namespaces` | Target specific namespaces | Adds `--namespaces` arg to filter collection |
| `custom-image` | Use different image/tag | Updates image reference for custom builds |

## Creating Your Own Overlay

Create a new directory with a `kustomization.yaml` that references the base:

```yaml
# my-overlay/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../base
  # Or reference from GitHub:
  # - https://github.com/redhat-developer/rhdh-must-gather/deploy/base?ref=main

# Customize namespace
namespace: my-namespace

# Customize image
images:
  - name: quay.io/rhdh-community/rhdh-must-gather
    newTag: v1.0.0

# Add patches for further customization
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: add
        path: /spec/template/spec/initContainers/0/args
        value:
          - "--with-secrets"
          - "--namespaces"
          - "my-rhdh-namespace"
```

## Common Customizations

### Change the image tag

```yaml
images:
  - name: quay.io/rhdh-community/rhdh-must-gather
    newTag: v1.2.3
```

### Change ephemeral volume storage size

```yaml
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: replace
        path: /spec/template/spec/volumes/0/ephemeral/volumeClaimTemplate/spec/resources/requests/storage
        value: 5Gi
```

### Add command-line arguments

```yaml
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: add
        path: /spec/template/spec/initContainers/0/args
        value:
          - "--namespaces"
          - "ns1,ns2"
          - "--without-operator"
```

### Change environment variables

```yaml
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: replace
        path: /spec/template/spec/initContainers/0/env/2/value
        value: "DEBUG"
```

### Change resource limits

```yaml
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: replace
        path: /spec/template/spec/initContainers/0/resources/limits/memory
        value: "1Gi"
      - op: replace
        path: /spec/template/spec/initContainers/0/resources/limits/cpu
        value: "1"
```

### Use a custom storage class

```yaml
patches:
  - target:
      kind: Deployment
      name: rhdh-must-gather
    patch: |
      - op: add
        path: /spec/template/spec/volumes/0/ephemeral/volumeClaimTemplate/spec/storageClassName
        value: my-storage-class
```

## Retrieving the Output

After the deployment is ready (the gather init container has completed), retrieve the collected data:

```bash
# Wait for the deployment to be available (gather init container must complete first)
kubectl -n rhdh-must-gather wait --for=condition=available deployment/rhdh-must-gather --timeout=3600s

# Get the data-holder pod name
POD_NAME=$(kubectl -n rhdh-must-gather get pod -l app=rhdh-must-gather,component=data-holder -o jsonpath='{.items[0].metadata.name}')

# Download the archive
kubectl -n rhdh-must-gather exec "$POD_NAME" -- tar czf - -C /must-gather . > rhdh-must-gather-output.tar.gz
```

## Cleanup

```bash
# Using default deployment
kubectl delete -k deploy/

# Using an overlay
kubectl delete -k deploy/overlays/debug-mode/

# From GitHub
kubectl delete -k https://github.com/redhat-developer/rhdh-must-gather/deploy?ref=main
```
