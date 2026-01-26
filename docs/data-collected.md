## What Data is Collected

This tool focuses exclusively on RHDH-related resources as well some very minimal platform about the cluster. For general cluster-wide information, combine this with the generic OCP must-gather.

### Platform Information
- **Platform Detection**: Automatically identifies the platform type:
    - **OpenShift**: OCP, ROSA (Red Hat OpenShift Service on AWS), ARO (Azure Red Hat OpenShift), ROKS (Red Hat OpenShift on IBM Cloud)
    - **Managed Kubernetes**: EKS (AWS), GKE (Google Cloud), AKS (Azure)
    - **Vanilla Kubernetes**: Standard Kubernetes installations
- **Infrastructure Detection**: Identifies underlying cloud providers (AWS, GCP, Azure, IBM Cloud, vSphere)
- **Version Information**: Collects OpenShift and Kubernetes version details

### RHDH-Specific Data

#### Helm Deployments
- **Release Information**: Helm releases, history, status
- **Configuration**: User-provided values, computed values, manifests, hooks, and notes
- **Kubernetes Manifests**: Deployments, StatefulSets with full YAML definitions and descriptions
- **[Application Runtime Data](#application-runtime-data-extracted-from-rhdh-containers-if-running)**

#### Operator Deployments
- **OLM Information**: ClusterServiceVersions, Subscriptions, InstallPlans, OperatorGroups, CatalogSources
- **Custom Resources**: Backstage CRDs with definitions and descriptions
- **Backstage Custom Resources**: Full CR configurations and status
- **Operator Infrastructure**: Deployments, logs, and configurations in operator namespaces
- **[Application Runtime Data](#application-runtime-data-extracted-from-rhdh-containers-if-running)**

#### Orchestrator-Flavored Deployments
RHDH can be deployed using the Orchestrator flavor, which includes additional infrastructure components. The must-gather tool automatically detects and collects information about these components:

- **OpenShift Serverless Operator** (in `openshift-serverless` namespace):
  - ClusterServiceVersions (CSV) with operator version information
  - Operator deployments, pods, and logs
  - OLM subscriptions for version tracking
  - Knative OpenShift and Knative OpenShift Ingress operator logs

- **OpenShift Serverless Logic Operator** (in `openshift-serverless-logic` namespace):
  - ClusterServiceVersions (CSV) with operator version information
  - Operator deployments, pods, and logs
  - OLM subscriptions for version tracking
  - Logic operator logs

- **Orchestrator CRDs**:
  - `sonataflowplatforms.sonataflow.org` - SonataFlowPlatform CRD
  - `sonataflows.sonataflow.org` - SonataFlow workflow CRD
  - `sonataflowclusterplatforms.sonataflow.org` - SonataFlowClusterPlatform CRD
  - `sonataflowbuilds.sonataflow.org` - SonataFlowBuild CRD
  - `knativeservings.operator.knative.dev` - KnativeServing CRD
  - `knativeeventings.operator.knative.dev` - KnativeEventing CRD
  - `knativekafkas.operator.serverless.openshift.io` - KnativeKafka CRD (optional)

- **SonataFlowPlatform Custom Resources**:
  - Full CR definitions and descriptions
  - Related deployments, services, and logs
  - Data Index Service information
  - SonataFlow workflows in the same namespace

- **Knative Resources**:
  - KnativeServing CRs and namespace resources (`knative-serving`)
  - KnativeEventing CRs and namespace resources (`knative-eventing`)
  - KnativeKafka CRs (if installed)

- **Orchestrator Summary**: A consolidated `summary.txt` file with:
  - OpenShift Serverless operator version
  - OpenShift Serverless Logic operator version
  - List of SonataFlowPlatform CRs and their status
  - List of SonataFlow workflows and their status
  - Knative Serving and Eventing status

**Note**: Orchestrator data collection can be skipped with `--without-orchestrator` flag if the Orchestrator flavor is not in use.

#### Application Runtime Data (extracted from RHDH containers, if running)
- **RHDH version information**: `backstage.json` contains Backstage version
- **Build metadata**: `build-metadata.json` with RHDH version, Backstage version, upstream/midstream sources, and build timestamp
- **Node.js version**: Runtime Node.js version from `node --version`
- **Container user ID**: Security context information from `id` command
- **Dynamic plugins structure**: Directory listing of `dynamic-plugins-root` filesystem
- **Running processes**: Complete list of all running processes in each container (collected via `/proc` filesystem enumeration)
  - Process ID (PID) and Parent Process ID (PPID)
  - Process state (R=running, S=sleeping, D=disk sleep, Z=zombie, T=stopped)
  - Memory usage (RSS and Virtual Size in KB)
  - Process name and full command line
  - Memory summary from `/proc/meminfo`
  - Useful for correlating with heap dumps to identify orphaned or zombie processes
- **Application configuration**
  - **Generated app-config**: `app-config.dynamic-plugins.yaml` created by the dynamic plugins installer
  - **Dynamic plugins**
    - **Dynamic plugins root directory** structure from filesystem (`ls -lhrta dynamic-plugins-root`)
    - **Generated app-config** from dynamic plugins installer (`app-config.dynamic-plugins.yaml`)
    - **ConfigMaps** containing app configurations and dynamic plugin definitions

#### Logs and Runtime Data
- **Container logs** with configurable time windows (`MUST_GATHER_SINCE`, `MUST_GATHER_SINCE_TIME`)
- **Multi-container logs**: Separate logs for `backstage-backend` and `install-dynamic-plugins` containers
- **Local Database logs** from PostgreSQL StatefulSets, unless the app is configured to connect to external databases
- **Must-gather container logs** (when running in pod)

#### RHDH Manifests (Detailed)
- **Deployments and StatefulSets**: Full YAML definitions and kubectl describe output
- **Pods**: Complete pod specifications, status, and logs for all related pods
- **ConfigMaps**: Application configurations, dynamic plugins, and other config data
- **Secrets**(opt-in with `--with-secrets`): Sanitized secret resources (data fields redacted for security)
- **Services, Routes, Ingresses**: Network configurations for RHDH access

### Namespace inspect (collected by default)
- **Deep namespace resource inspection** using `oc adm inspect namespace` (included by default)
- **Auto-detects RHDH namespaces**:
  - Namespaces with Helm-based RHDH deployments
  - Namespaces with Backstage Custom Resources (operator-based)
  - **RHDH operator namespace(s)** automatically included
- **OMC-compatible output** - works with [OpenShift Must-Gather Client (OMC)](https://github.com/gmeghnag/omc) for interactive analysis
- **Comprehensive resource collection** including:
  - All Kubernetes resources in YAML format
  - Pod logs (current and previous containers)
  - Events timeline for troubleshooting
  - Resource descriptions and status
- **Can be disabled** with `--without-namespace-inspect` flag (not recommended - removes OMC compatibility)

### Heap Dumps (opt-in, disabled by default)
- **Memory diagnostics** from running backstage-backend containers using `--with-heap-dumps`
- **Integrated collection**: Heap dumps are collected automatically **right after pod logs** for each Helm release and Backstage CR
- **Process metadata**: Memory usage, Node.js version, disk space, and process information collected alongside dumps
- **Use cases**: Memory leak troubleshooting, performance analysis, and OOM investigations
- **File format**: `.heapsnapshot` files compatible with Chrome DevTools and other heap analysis tools
- **Important considerations**:
  - Requires application to handle SIGUSR2 signal. Choose ONE of:
    - ⭐ `NODE_OPTIONS="--heapsnapshot-signal=SIGUSR2 --diagnostic-dir=/tmp"`
  - **Note**: `--diagnostic-dir=/tmp` because the root filesystem is read-only
  - Heap dumps can be very large (100MB-1GB+ per pod) and take several minutes per deployment
  - Success rate depends on application instrumentation

### Cluster Information (optional)
- **Cluster-wide diagnostic dump** using `oc cluster-info dump` (enabled with `--cluster-info` flag)
