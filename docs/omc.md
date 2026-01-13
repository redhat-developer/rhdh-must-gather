## Using with OMC (OpenShift Must-Gather Client)

The Namepace's inspect output is fully compatible with [OMC (OpenShift Must-Gather Client)](https://github.com/gmeghnag/omc), a powerful tool for interactive must-gather analysis used by Support teams.

**Note**: Namepace's inspect is **collected by default**, so all must-gather outputs are OMC-compatible.

### Setup

1. **Collect data** (Namepace's inspect included by default):
   ```bash
   oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather
   ```

2. **Install OMC** (if not already installed):
   ```bash
   curl -sL https://github.com/gmeghnag/omc/releases/latest/download/omc_$(uname)_$(uname -m).tar.gz | tar xzf - omc
   chmod +x ./omc
   sudo mv ./omc /usr/local/bin/
   ```

### Using OMC with Namepace's inspect Data

Point OMC to the Namepace's inspect directory:

```bash
# Navigate to your must-gather output
cd must-gather.local.*/

# Use OMC with the Namepace's inspect directory
omc use namespace-inspect

# Now query resources interactively (OMC will see all inspected namespaces)
omc get pods --all-namespaces
omc get pods -n rhdh-prod
omc get deployments -n rhdh-staging -o wide
omc get events --sort-by='.lastTimestamp'
```

### OMC Examples for RHDH Troubleshooting

```bash
# List all pods with their node assignments
omc get pods -o wide

# Get pods by label
omc get pods -l app.kubernetes.io/name=backstage

# Retrieve deployment details
omc get deployment backstage-bs1 -o yaml

# Check events for a specific pod
omc get events --field-selector involvedObject.name=<pod-name>

# Get all resources of a specific type
omc get configmaps -o name

# Use JSONPath queries
omc get pods -o jsonpath="{.items[*].metadata.name}"
```

### Directory Structure for OMC

The Namepace's inspect creates OMC-compatible directory structures:

```
namespace-inspect/            # ← Point OMC here: omc use namespace-inspect
├── namespaces/               # All inspected namespaces in one place
│   ├── rhdh-prod/           # First namespace
│   │   ├── apps/            # Deployments, StatefulSets, etc.
│   │   ├── core/            # Pods, Services, ConfigMaps, etc.
│   │   ├── batch/           # Jobs, CronJobs
│   │   └── networking.k8s.io/
│   ├── rhdh-staging/        # Second namespace
│   │   └── [same structure]
│   └── ...                  # Additional namespaces
├── event-filter.html
└── aggregated-discovery-apis.yaml
```
