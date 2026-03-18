# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

RHDH Must-Gather is a diagnostic data collection tool for Red Hat Developer Hub (RHDH) deployments on Kubernetes and OpenShift clusters. It collects logs, configurations, and resources from both Helm-based and Operator-managed RHDH instances.

## Common Commands

### Development and Testing
```bash
make run-local              # Run collection locally (requires kubectl/oc, helm, jq, yq, cluster access)
make run-script SCRIPT=helm # Test a specific gather_* script (e.g., helm, operator, orchestrator)
make test                   # Run BATS unit tests
make test-e2e               # Run E2E tests in local mode against a K8s cluster
make test-e2e LOCAL=false   # Run E2E tests using container image
```

### Building
```bash
make image-build                    # Build container image with podman
make image-push REGISTRY=quay.io IMAGE_NAME=org/image IMAGE_TAG=v1.0.0
```

### Deployment Testing
```bash
make deploy-openshift                               # Test with `oc adm must-gather`
make deploy-k8s                                     # Test on standard Kubernetes
make deploy-k8s OVERLAY=with-heap-dumps             # Use a kustomize overlay
make deploy-k8s OPTS="--with-secrets --namespaces ns1,ns2"
```

### Cleanup
```bash
make clean                  # Remove images, tools, and test output
make clean-out              # Remove only the local output directory (./out)
```

## Architecture

### Collection Scripts (`collection-scripts/`)
- **`must_gather`** - Main orchestrator; parses flags, runs collectors in sequence, triggers sanitization on exit
- **`common.sh`** - Shared utilities: logging, namespace filtering, `safe_exec()` for timeout-wrapped commands, `collect_rhdh_data()` for app introspection
- **`gather_*`** - Individual collectors (helm, operator, orchestrator, platform, route, ingress, namespace-inspect, cluster-info)
- **`sanitize`** - Post-collection data sanitization (secrets, tokens, SSH keys, passwords)
- **`logs.sh`** - Collects must-gather container logs when running in a pod

### Collection Flow
1. `must_gather` parses CLI flags and exports env vars (`RHDH_TARGET_NAMESPACES`, `RHDH_WITH_SECRETS`, `RHDH_WITH_HEAP_DUMPS`)
2. Runs each enabled `gather_*` script sequentially
3. On exit (success or interrupt), runs `sanitize` to redact sensitive data
4. Outputs to `BASE_COLLECTION_PATH` (default: `/must-gather` in container, `./out` locally)

### Deployment Manifests (`deploy/`)
Kustomize-based manifests for running on standard Kubernetes:
- `deploy/base/` - Core resources (Deployment, RBAC, ServiceAccount)
- `deploy/overlays/` - Pre-built configurations (debug-mode, with-heap-dumps, specific-namespaces)

### Tests (`tests/`)
- **Unit tests**: `tests/*.bats` - BATS tests for shell functions (use `tests/test_helper.bash` for setup)
- **E2E tests**: `tests/e2e/` - Full cluster-based tests with Kind
  - `run-e2e-tests.sh` - Test runner
  - `validate-*.sh` - Validation scripts for different deployment types

## Key Patterns

### Adding a New Collector
1. Create `collection-scripts/gather_<name>` (executable, no extension)
2. Source `common.sh` for utilities
3. Add to `mandatory_scripts` array in `must_gather` if it should run by default
4. Use `safe_exec` for all external commands (provides timeout and error handling)
5. Respect `RHDH_TARGET_NAMESPACES` via `should_include_namespace()` and `get_namespace_args()`
6. Respect `RHDH_WITH_SECRETS` when collecting secrets

### Safe Command Execution
Always use `safe_exec` for kubectl/helm commands:
```bash
safe_exec "$KUBECTL_CMD -n '$ns' get pods -o yaml" "$output_dir/pods.yaml" "Description"
```

### Namespace Filtering
```bash
if ! should_include_namespace "$ns"; then
    log_debug "Skipping namespace $ns"
    continue
fi
```

## Commit Guidelines

Follow Conventional Commits format with required body and trailers:

```
<type>(<scope>): <subject>

<body>

<trailers>
```

### Subject Line
- Use conventional commit format: `<type>(<scope>): <subject>`
- Types: `feat`, `fix`, `refactor`, `docs`, `chore`, `test`, `ci`
- Keep under 72 characters
- Use imperative mood (e.g., "add" not "added")

### Body (Required)
- **Must include context explaining WHY the change was made**
- Separate from subject with a blank line
- Wrap at 72 characters
- Explain: What problem does this solve? Why this approach? What are the implications?

### Trailers (Required)
- Must include: `Assisted-by: Claude`
- Other trailers as needed (e.g., `Co-authored-by`, `Fixes`, etc.)

### Example

```
feat(ci): add expiry labels to commit-SHA tagged images

Update both PR and release workflows to rebuild and push extra tags
(those with commit SHA) using make build-push instead of simple
docker tag/push. This ensures the quay.expires-after=2w label is
properly applied to ephemeral commit-specific tags.

Main stable tags (next, next-1.x, pr-{number}, and version tags)
remain permanent, while commit-SHA variants (next-{sha}, next-1.x-{sha},
pr-{number}-{sha}) now automatically expire after 2 weeks to reduce
registry clutter.

Assisted-by: Claude
```

### Checklist
- Does the subject line clearly describe WHAT changed?
- Does the body explain WHY this change was needed?
- Is the `Assisted-by: Claude` trailer included?
- Would someone reading this in 6 months understand the reasoning?

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_COLLECTION_PATH` | `/must-gather` | Output directory |
| `LOG_LEVEL` | `info` | Logging level (info, debug, trace) |
| `CMD_TIMEOUT` | `90` | Timeout for kubectl/helm commands (seconds) |
| `RHDH_TARGET_NAMESPACES` | - | Comma-separated namespace filter |
| `RHDH_WITH_SECRETS` | `false` | Include secrets in collection |
| `RHDH_WITH_HEAP_DUMPS` | `false` | Collect heap dumps from Node.js processes |
