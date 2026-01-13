# RHDH Must-Gather Tool (WIP)

A specialized diagnostic data collection tool for Red Hat Developer Hub (RHDH) deployments on Kubernetes and OpenShift clusters.

## Overview

This tool helps support teams and engineers collect essential RHDH-specific information to troubleshoot issues effectively. It focuses exclusively on RHDH resources and can be combined with generic cluster information collection. It supports:

- **Multi-platform**: OpenShift and standard Kubernetes
- **Multi-deployment**: Helm-based and Operator-based RHDH instances
- **RHDH-focused collection**: Only RHDH-specific logs, configurations, and resources

> **Note**: For more general cluster-wide information, combine this with the generic OpenShift must-gather: `oc adm must-gather`

## Quick Start

### Using with OpenShift (`oc adm must-gather`)

```bash
# Use the published image
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather

# Collect relevant RHDH data and logs and events from last 2 hours
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather --since=2h

# Collect relevant RHDH data and logs and events since specific time
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather --since-time=2025-08-21T20:00:00Z

# To pass specific options to the gather script
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- /usr/bin/gather [options...]
```

### Using with Kubernetes (`Kustomize`)

```bash
# Basic deployment using the default configuration
kubectl apply -k 'https://github.com/redhat-developer/rhdh-must-gather/deploy?ref=main'

# Wait for job completion
kubectl -n rhdh-must-gather wait --for=condition=complete job/rhdh-must-gather \
  --timeout=600s

# Wait for the data retriever pod to be ready
kubectl -n rhdh-must-gather wait --for=condition=ready pod/rhdh-must-gather-data-retriever \
  --timeout=60s

# Stream the tar archive from the pod
kubectl -n rhdh-must-gather exec rhdh-must-gather-data-retriever -- \
  tar czf - -C /data . > rhdh-must-gather-output.k8s.tar.gz

# Clean up
kubectl delete -k 'https://github.com/redhat-developer/rhdh-must-gather/deploy?ref=main'
```

**Using pre-built overlays:**

```bash
# Enable debug mode with increased resources
kubectl apply -k 'https://github.com/redhat-developer/rhdh-must-gather/deploy/overlays/debug-mode?ref=main'

# Enable heap dump collection (larger storage, extended timeout)
kubectl apply -k 'https://github.com/redhat-developer/rhdh-must-gather/deploy/overlays/with-heap-dumps?ref=main'
```

**Creating your own overlay for custom configurations:**

```bash
# Create a local overlay directory
mkdir -p my-must-gather-overlay

# Create a kustomization.yaml that references the base
cat > my-must-gather-overlay/kustomization.yaml <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - 'https://github.com/redhat-developer/rhdh-must-gather/deploy/base?ref=main'

# Example: Change the namespace
namespace: my-custom-namespace

# Example: Use a specific image tag
images:
  - name: quay.io/rhdh-community/rhdh-must-gather
    newTag: v1.0.0

patches:
  # Example: Add custom arguments to the gather script
  - target:
      kind: Job
      name: rhdh-must-gather
    patch: |
      - op: add
        path: /spec/template/spec/containers/0/args
        value:
          - "--namespaces"
          - "rhdh-prod,rhdh-staging"
          - "--with-secrets"
EOF

# Apply your custom overlay
kubectl apply -k my-must-gather-overlay/
```

See the [deploy/overlays](deploy/overlays) directory for more overlay examples.

## What Data is Collected

See [data-collected.md](./docs/data-collected.md) for more details.

## Using with OMC (OpenShift Must-Gather Client)

See [omc.md](./docs/omc.md) for more details.

## Analyzing Heap Dumps

See [heap-dumps-collection.md](./docs/heap-dumps-collection.md) for more details.

## Secrets Collection and Sanitization (Opt-In by default)

See [secret-collection-and-sanitization.md](./docs/secret-collection-and-sanitization.md) for more details.

## Configuration

### Environment Variables

| Variable                 | Default         | Description                                            |
|--------------------------|-----------------|--------------------------------------------------------|
| `BASE_COLLECTION_PATH`   | `/must-gather`  | Output directory for collected data                    |
| `LOG_LEVEL`              | `info`          | Logging level (info, debug, trace)                     |
| `CMD_TIMEOUT`            | `30`            | Timeout for individual kubectl/helm commands (seconds) |
| `MUST_GATHER_SINCE`      | -               | Relative time for log collection (e.g., "2h", "30m")   |
| `MUST_GATHER_SINCE_TIME` | -               | Absolute timestamp for log collection (RFC3339)        |

### Command Line Options

```bash
Usage: ./must_gather [params...]

  A client tool for gathering RHDH information from Helm and Operator installations in an OpenShift or Kubernetes cluster

  Available options:

  > To see this help menu and exit, use:
  --help

  > By default, the tool collects RHDH-specific information including:
  > - platform
  > - helm
  > - operator
  > - orchestrator
  > - route
  > - ingress
  > - namespace-inspect

  > You can exclude specific data collection types:
  --without-operator            Skip operator-based RHDH deployment data collection
  --without-orchestrator        Skip Orchestrator-flavored deployment data collection
                                (OpenShift Serverless, Serverless Logic, SonataFlowPlatform)
  --without-helm                Skip Helm-based RHDH deployment data collection  
  --without-platform            Skip platform detection and information
  --without-route               Skip OpenShift route collection
  --without-ingress             Skip Kubernetes ingress collection
  --without-namespace-inspect   Skip deep Namepace's inspect

  > You can also choose to enable optional collectors:
  --cluster-info                Collect cluster-wide diagnostic information

  > You can limit collection to specific namespaces:
  --namespaces ns1,ns2    Collect data only from specified comma-separated namespaces

  > Security and Privacy Options:
  --with-secrets                Include Kubernetes Secrets in collection (opt-in, disabled by default)
                                When disabled, secret resources are excluded from all collectors
                                When enabled, secrets are collected but automatically sanitized

  > Diagnostic and Troubleshooting Options:
  --with-heap-dumps             Collect heap dumps from running backstage-backend processes (opt-in, disabled by default)
                                Heap dumps are collected immediately after pod logs for each deployment/CR
                                Useful for troubleshooting memory leaks and performance issues
                                
                                IMPORTANT: Requires NODE_OPTIONS environment variable:
                                  NODE_OPTIONS=--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp
                                
                                Why these flags?
                                  • --heapsnapshot-signal=SIGUSR2: Built into Node.js v12.0.0+, enables heap dumps
                                  • --diagnostic-dir=/tmp: REQUIRED for read-only root filesystems
                                
                                No image rebuild or source code changes needed!
                                
                                Collection method: SIGUSR2 signal sent directly via kubectl exec
                                Works with any Kubernetes version, no special RBAC permissions needed
                                Warning: May take several minutes and generate large files (100MB-1GB+ per pod)

  Examples:
  # Default collection (includes Namepace's inspect for OMC compatibility)
  ./must_gather

  # Collect only Helm data (skip operator data)
  ./must_gather --without-operator

  # Collect only operator data (skip Helm data)
  ./must_gather --without-helm

  # Skip Namepace's inspect (not recommended - removes OMC compatibility)
  ./must_gather --without-namespace-inspect

  # Minimal collection (only platform info, no Namepace's inspect)
  ./must_gather --without-operator --without-helm --without-route --without-ingress --without-namespace-inspect

  # Collect from specific namespaces only
  ./must_gather --namespaces rhdh-prod,rhdh-staging

  # Combine namespace filtering with exclusions
  ./must_gather --namespaces my-rhdh-ns --without-operator

  # Include secrets in collection (for detailed troubleshooting - secrets will be sanitized)
  ./must_gather --with-secrets

  # Collect heap dumps for memory troubleshooting (requires app configured with NODE_OPTIONS)
  # Prerequisites: Add NODE_OPTIONS=--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp to the backsetage-backend container
  ./must_gather --with-heap-dumps

  # Full diagnostic collection (secrets + heap dumps - generates large output)
  ./must_gather --with-secrets --with-heap-dumps
```

#### Available Exclusion Flags

| Flag | Description | Use Case |
|------|-------------|----------|
| `--without-operator` | Skip operator-based RHDH deployment data | When you know RHDH is deployed via Helm only |
| `--without-orchestrator` | Skip Orchestrator-related data (Serverless, Serverless Logic, SonataFlowPlatform) | When you know you don't have any Orchestrator flavor instances of RHDH |
| `--without-helm` | Skip Helm-based RHDH deployment data | When you know RHDH is deployed via Operator only |
| `--without-platform` | Skip platform detection and information | For minimal collections when platform info is not needed |
| `--without-route` | Skip OpenShift route collection | For non-OpenShift clusters or when routes are not relevant |
| `--without-ingress` | Skip Kubernetes ingress collection | When ingresses are not used for RHDH access |
| `--without-namespace-inspect` | Skip deep Namepace's inspect | **Not recommended** - removes OMC compatibility. Use only for minimal/quick collections |

#### Namespace Filtering

| Flag | Description | Use Case |
|------|-------------|----------|
| `--namespaces ns1,ns2` | Limit collection to specified comma-separated namespaces | When RHDH is deployed in specific known namespaces |
| `--namespaces=ns1,ns2` | Alternative syntax for namespace filtering | Same as above with equals syntax |

**Examples:**
- `--namespaces rhdh-prod,rhdh-staging` - Collect only from production and staging namespaces
- `--namespaces=my-rhdh-ns` - Collect only from a single namespace
- Combine with exclusions: `--namespaces prod-ns --without-helm` - Only operator data from prod-ns

#### Optional Feature Flags

| Flag | Description | Use Case |
|------|-------------|----------|
| `--cluster-info` | Collect cluster-wide diagnostic information | For comprehensive cluster analysis |
| `--with-secrets` | Include Kubernetes Secrets (sanitized) | For detailed troubleshooting requiring secret metadata |
| `--with-heap-dumps` | Collect heap dumps from backstage-backend containers | For memory leak investigation and performance analysis |

**Examples:**
- `--with-heap-dumps` - Collect heap dumps for all backstage-backend pods
- `--with-secrets --with-heap-dumps` - Full diagnostic collection
- `--namespaces prod-ns --with-heap-dumps` - Heap dumps from specific namespace only

## Output Structure

<details>

<summary>Click to expand</summary>

```
/must-gather/
├── version                         # Tool version information (e.g., "rhdh-must-gather x.y.z-sha")
├── sanitization-report.txt         # Data sanitization summary and details
├── all-routes.txt                  # All OpenShift routes cluster-wide
├── all-ingresses.txt               # All Kubernetes ingresses cluster-wide
├── must-gather.log                 # Must-gather container logs (if running in pod)
├── cluster-info/                   # Cluster-wide information (if --cluster-info used)
│   └── [cluster-info dump output]
├── namespace-inspect/              # Deep Namepace's inspect (collected by default)
│   ├── inspect.log                 # Inspection command logs
│   ├── inspection-summary.txt      # Summary of inspected namespaces and data collected
│   ├── namespaces/                 # All inspected namespaces (OMC-compatible structure)
│   │   ├── [namespace-1]/          # First namespace (e.g., "rhdh-prod")
│   │   │   ├── [namespace].yaml    # Namespace definition
│   │   │   ├── apps/               # Application resources (Deployments, StatefulSets, etc.)
│   │   │   ├── core/               # Core resources (ConfigMaps, Secrets, Services, etc.)
│   │   │   ├── networking.k8s.io/  # Network policies and configurations
│   │   │   ├── batch/              # Jobs and CronJobs
│   │   │   ├── autoscaling/        # HPA and scaling configurations
│   │   │   └── pods/               # Detailed pod information with logs
│   │   │       └── [pod-name]/
│   │   │           ├── [pod-name].yaml
│   │   │           └── [container-name]/
│   │   │               └── logs/
│   │   │                   ├── current.log
│   │   │                   ├── previous.log
│   │   │                   └── previous.insecure.log
│   │   ├── [namespace-2]/          # Second namespace (e.g., "rhdh-staging")
│   │   │   └── [same structure as above]
│   │   └── [namespace-N]/          # Additional namespaces...
│   ├── aggregated-discovery-api.yaml
│   ├── aggregated-discovery-apis.yaml
│   └── event-filter.html           # Events visualization
├── platform/                       # Platform and infrastructure information
│   ├── platform.json               # Structured platform data (platform, underlying, versions)
│   └── platform.txt                # Human-readable platform summary
├── helm/                           # Helm deployment data (native releases + standalone)
│   ├── all-rhdh-releases.txt       # List of all detected RHDH deployments (native + standalone)
│   ├── releases/                   # Native Helm releases (tracked by Helm)
│   │   └── ns=[namespace]/         # Per-namespace organization
│   │       ├── _configmaps/        # Namespace-wide ConfigMaps with both formats
│   │       │   ├── [configmap-name].yaml               # Full ConfigMap YAML
│   │       │   └── [configmap-name].describe.txt       # kubectl describe output
│   │       ├── _secrets/           # Namespace-wide Secrets (sanitized)
│   │       │   ├── [secret-name].yaml                  # Full Secret YAML (sanitized)
│   │       │   └── [secret-name].describe.txt          # kubectl describe output (data redacted)
│   │       └── [release-name]/     # Per-release directory
│   │           ├── values.yaml         # User-provided values
│   │           ├── all-values.yaml     # All computed values (25KB+ files)
│   │           ├── manifest.yaml       # Deployed manifest (18KB+ files)
│   │           ├── hooks.yaml          # Helm hooks
│   │           ├── history.txt         # Release history
│   │           ├── history.yaml        # Release history (YAML)
│   │           ├── status.txt          # Release status (text)
│   │           ├── status.yaml         # Release status (YAML, 21KB+ files)
│   │           ├── notes.txt           # Release notes
│   │           ├── deployment/         # Application deployment info
│   │           │   ├── deployment.yaml
│   │           │   ├── deployment.describe.txt
│   │           │   ├── app-container-userid.txt      # "uid=1001 gid=0(root) groups=0(root)"
│   │           │   ├── backstage.json              # {"version": "1.39.1"}
│   │           │   ├── build-metadata.json         # RHDH version, Backstage version, source repos, build time
│   │           │   ├── node-version.txt            # "v22.16.0"
│   │           │   ├── dynamic-plugins-root.fs.txt # Directory listing with plugin packages
│   │           │   ├── app-config.dynamic-plugins.yaml # Generated app config (9KB files)
│   │           │   ├── logs-app.txt                # All container logs (2MB+ files)
│   │           │   ├── logs-app--backstage-backend.txt # Backend logs (2MB+ files)
│   │           │   ├── logs-app--install-dynamic-plugins.txt # Init container logs (17KB files)
│   │           │   ├── heap-dumps/     # Memory heap dumps (if --with-heap-dumps used)
│   │           │   │   └── pod=[pod-name]/         # Per-pod directory
│   │           │   │       └── container=[container-name]/
│   │           │   │           ├── heapdump-[timestamp].heapsnapshot  # Heap dump (100MB-1GB+)
│   │           │   │           ├── process-info.txt        # Process and memory info
│   │           │   │           ├── heap-dump.log           # Collection logs
│   │           │   │           └── pod-spec.yaml           # Pod specification
│   │           │   ├── processes/      # Process list from running pods (all replicas)
│   │           │   │   └── pod=[pod-name]/         # Per-pod directory
│   │           │   │       └── container=[container-name].txt  # Process list per container
│   │           │   └── pods/           # Pod details and logs
│   │           │       ├── pods.txt
│   │           │       ├── pods.yaml
│   │           │       └── pods.describe.txt
│   │           └── db-statefulset/     # Database StatefulSet info (if database enabled)
│   │               ├── db-statefulset.yaml
│   │               ├── db-statefulset.describe.txt
│   │               ├── logs-db.txt     # Database logs
│   │               └── pods/           # Database pod details
│   │                   ├── pods.txt
│   │                   ├── pods.yaml
│   │                   └── pods.describe.txt
│   └── standalone/                 # Standalone Helm deployments (helm template + kubectl apply)
│       └── ns=[namespace]/         # Per-namespace organization
│           └── [workload-name]/    # Per-workload directory (Deployment or StatefulSet name)
│               ├── standalone-note.txt   # Explanation of standalone detection
│               ├── helm-metadata.txt     # Extracted Helm labels (chart, instance, version)
│               ├── deployment.yaml       # Deployment YAML (or statefulset.yaml)
│               ├── deployment.describe.txt
│               ├── deployment/           # Application deployment info (same as native releases)
│               │   ├── logs-app.txt
│               │   ├── pods/
│               │   └── processes/
│               └── dependencies/         # Dependent services (e.g., PostgreSQL from subchart)
│                   └── [dep-name]/       # Per-dependency directory
│                       ├── statefulset.yaml    # Dependency workload YAML
│                       ├── statefulset.describe.txt
│                       └── logs-[pod].txt      # Dependency logs
├── orchestrator/                   # Orchestrator-flavored deployment data (if detected)
│   ├── summary.txt                 # Summary of all detected Orchestrator components and versions
│   ├── serverless-operators/       # OpenShift Serverless operators information
│   │   ├── ns=openshift-serverless/        # OpenShift Serverless operator namespace
│   │   │   ├── csv-list.txt                # ClusterServiceVersion list (operator version)
│   │   │   ├── csv-all.yaml                # CSV details
│   │   │   ├── deployments.txt             # Operator deployments
│   │   │   ├── pods.txt                    # Operator pods
│   │   │   ├── subscriptions.txt           # OLM subscriptions
│   │   │   └── logs-*.txt                  # Operator logs
│   │   └── ns=openshift-serverless-logic/  # OpenShift Serverless Logic operator namespace
│   │       ├── csv-list.txt                # ClusterServiceVersion list (operator version)
│   │       ├── csv-all.yaml                # CSV details
│   │       ├── deployments.txt             # Operator deployments
│   │       ├── pods.txt                    # Operator pods
│   │       ├── subscriptions.txt           # OLM subscriptions
│   │       └── logs-logic-operator.txt     # Logic operator logs
│   ├── crds/                       # Orchestrator-related Custom Resource Definitions
│   │   ├── found-crds.txt          # List of detected Orchestrator CRDs
│   │   ├── sonataflowplatforms.sonataflow.org.yaml     # SonataFlowPlatform CRD
│   │   ├── sonataflows.sonataflow.org.yaml             # SonataFlow CRD
│   │   ├── knativeservings.operator.knative.dev.yaml   # KnativeServing CRD
│   │   └── knativeeventings.operator.knative.dev.yaml  # KnativeEventing CRD
│   ├── sonataflow-platforms/       # SonataFlowPlatform Custom Resources
│   │   ├── all-sonataflow-platforms.txt    # List of all SonataFlowPlatform CRs
│   │   └── ns=[namespace]/         # Per-namespace SonataFlowPlatform data
│   │       └── [platform-name]/    # Per-platform directory
│   │           ├── [platform-name].yaml    # SonataFlowPlatform CR definition
│   │           ├── describe.txt            # CR description
│   │           ├── related-deployments.txt # Platform-related deployments
│   │           ├── related-services.txt    # Platform-related services
│   │           ├── logs.txt                # Platform-related logs
│   │           └── workflows/              # SonataFlow workflows in this namespace
│   │               ├── all-workflows.txt   # List of workflows
│   │               └── [workflow-name]/    # Per-workflow directory
│   │                   ├── workflow.yaml   # SonataFlow workflow definition
│   │                   ├── describe.txt    # Workflow description
│   │                   ├── pods.txt        # Workflow pods
│   │                   └── logs.txt        # Workflow logs
│   └── knative/                    # Knative resources
│       ├── knative-serving-list.txt        # KnativeServing CRs list
│       ├── knative-serving.yaml            # KnativeServing CR details
│       ├── knative-eventing-list.txt       # KnativeEventing CRs list
│       ├── knative-eventing.yaml           # KnativeEventing CR details
│       ├── knative-serving/                # knative-serving namespace resources
│       │   ├── deployments.txt
│       │   ├── pods.txt
│       │   └── services.txt
│       └── knative-eventing/               # knative-eventing namespace resources
│           ├── deployments.txt
│           ├── pods.txt
│           └── services.txt
└── operator/                       # Operator deployment data (if RHDH operators found)
    ├── all-deployments.txt         # List of all RHDH operator deployments
    ├── olm/                        # OLM information
    │   ├── rhdh-csv-all.txt        # ClusterServiceVersions
    │   ├── rhdh-subscriptions-all.txt # Subscriptions
    │   ├── installplans-all.txt     # InstallPlans
    │   ├── operatorgroups-all.txt   # OperatorGroups
    │   └── catalogsources-all.txt   # CatalogSources
    ├── crds/                       # Custom Resource Definitions
    │   ├── all-crds.txt            # All CRDs in cluster
    │   ├── backstages.rhdh.redhat.com.yaml # RHDH CRD definition
    │   └── backstages.rhdh.redhat.com.describe.txt # CRD description
    ├── ns=[operator-namespace]/     # Per-operator-namespace data (e.g., ns=rhdh-operator)
    │   ├── all-resources.txt       # All resources in namespace
    │   ├── configs/                # ConfigMaps with both formats
    │   │   ├── all-configmaps.txt
    │   │   ├── [configmap-name].yaml       # Full ConfigMap YAML
    │   │   └── [configmap-name].describe.txt # kubectl describe output
    │   ├── deployments/            # Operator deployments
    │   │   ├── all-deployments.txt
    │   │   ├── [deployment-selector].yaml
    │   │   └── [deployment-selector].describe.txt
    │   └── logs.txt               # Operator logs
    └── backstage-crs/              # Backstage Custom Resources
        ├── all-backstage-crs.txt   # List of all Backstage CRs
        └── ns=[cr-namespace]/      # Per-CR-namespace data (where Backstage CRs are deployed)
            ├── _configmaps/        # Namespace-wide ConfigMaps with both formats
            │   ├── [configmap-name].yaml               # Full ConfigMap YAML
            │   └── [configmap-name].describe.txt       # kubectl describe output
            ├── _secrets/           # Namespace-wide Secrets (sanitized)
            │   ├── [secret-name].yaml                  # Full Secret YAML (sanitized)
            │   └── [secret-name].describe.txt          # kubectl describe output (data redacted)
            └── [cr-name]/          # Per-CR directory
                ├── [cr-name].yaml      # CR definition
                ├── describe.txt        # CR description
                ├── deployment/         # Application deployment (same structure as Helm)
                │   ├── deployment.yaml
                │   ├── deployment.describe.txt
                │   ├── app-container-userid.txt      # "uid=1001 gid=0(root) groups=0(root)"
                │   ├── backstage.json              # {"version": "1.39.1"}
                │   ├── build-metadata.json         # RHDH version, Backstage version, source repos, build time
                │   ├── node-version.txt            # "v22.16.0"
                │   ├── dynamic-plugins-root.fs.txt # Directory listing with plugin packages
                │   ├── app-config.dynamic-plugins.yaml # Generated app config (9KB files)
                │   ├── logs-app.txt                # All container logs (2MB+ files)
                │   ├── logs-app--backstage-backend.txt # Backend logs (2MB+ files)
                │   ├── logs-app--install-dynamic-plugins.txt # Init container logs (17KB files)
                │   ├── heap-dumps/     # Memory heap dumps (if --with-heap-dumps used)
                │   │   └── pod=[pod-name]/         # Per-pod directory
                │   │       └── container=[container-name]/
                │   │           ├── heapdump-[timestamp].heapsnapshot  # Heap dump (100MB-1GB+)
                │   │           ├── process-info.txt        # Process and memory info
                │   │           ├── heap-dump.log           # Collection logs
                │   │           └── pod-spec.yaml           # Pod specification
                │   ├── processes/      # Process list from running pods (all replicas)
                │   │   └── pod=[pod-name]/         # Per-pod directory
                │   │       └── container=[container-name].txt  # Process list per container
                │   └── pods/           # Application pods
                │       ├── pods.txt
                │       ├── pods.yaml
                │       └── pods.describe.txt
                └── db-statefulset/     # Database StatefulSet (if database enabled)
                    ├── db-statefulset.yaml
                    ├── db-statefulset.describe.txt
                    ├── logs-db.txt     # Database logs
                    └── pods/           # Database pods
                        ├── pods.txt
                        ├── pods.yaml
                        └── pods.describe.txt
```

</details>

> **Note**: The tool automatically detects and collects data for both Helm and Operator-based RHDH deployments. For cluster-wide information, use the `--cluster-info` flag or combine with standard `oc adm must-gather`.

## Contributing and reporting issues

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
