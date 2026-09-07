# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

RHDH Must-Gather is a diagnostic data collection tool for Red Hat Developer Hub (RHDH) deployments on Kubernetes and OpenShift clusters. It collects logs, configurations, and resources from both Helm-based and Operator-managed RHDH instances.

## Common Commands

### Development and Testing
```bash
make run-local              # Build and run the Go gather binary locally (requires cluster access)
make test                   # Run Go unit tests
make lint                   # Run Go linter (golangci-lint)
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
make deploy-k8s                                     # Test on standard Kubernetes (uses Helm chart)
make deploy-k8s OPTS="--with-secrets --namespaces ns1,ns2"
```

### Cleanup
```bash
make clean                  # Remove images, tools, and test output
make clean-out              # Remove only the local output directory (./out)
```

## Architecture

### Go Binary (`cmd/gather/`, `internal/`)
The tool is a single Go binary that handles CLI parsing, Kubernetes API access, Helm SDK integration, and all collection logic natively. No external tools (kubectl, oc, helm, jq, yq, websocat) are needed at runtime.

- **`cmd/gather/main.go`** - Entry point
- **`internal/cli/`** - Cobra CLI, flag parsing, orchestration
- **`internal/collector/`** - Individual collectors (helm, operator, orchestrator, platform, route, ingress, namespace-inspect, cluster-info)
- **`internal/sanitize/`** - Post-collection data sanitization
- **`internal/exec/`** - Command execution utilities
- **`internal/log/`** - Logging utilities

### Collection Flow
1. Go binary parses CLI flags (`--namespaces`, `--with-secrets`, `--with-heap-dumps`, etc.)
2. Runs each enabled collector sequentially using Go SDK clients
3. On exit (success or interrupt), runs sanitization to redact sensitive data
4. Outputs to `BASE_COLLECTION_PATH` (default: `/must-gather` in container, `./out` locally)
5. Namespace inspection uses the `openshift/oc` inspect library for OMC-compatible output

### Tests (`tests/`)
- **Go unit tests**: `internal/**/*_test.go` - Go tests for collectors and utilities
- **E2E tests**: `tests/e2e/` - Full cluster-based tests with Kind
  - `run-e2e-tests.sh` - Test runner
  - `validate-*.sh` - Validation scripts for different deployment types

## Key Patterns

### Adding a New Collector
1. Create a new Go type implementing the collector interface in `internal/collector/`
2. Register it in the orchestrator's collector list in `internal/cli/gather.go`
3. Use the Kubernetes client-go SDK for API access (no shell-outs)
4. Respect `Config.TargetNamespaces` for namespace filtering
5. Respect `Config.WithSecrets` when collecting secrets

### PR Workflow Path Filtering
Workflows triggered by `pull_request` must **not** use `on.pull_request.paths` filtering — it prevents the workflow from firing at all, which blocks required status checks. Instead, always trigger the workflow and use `tj-actions/changed-files` as a step inside each job to gate subsequent steps:
```yaml
on:
  pull_request:
    branches: [main]
  # push triggers may still use paths filtering — only pull_request is affected

jobs:
  my-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@...
      - name: Get changed files
        id: changed-files
        uses: tj-actions/changed-files@...
        with:
          files: |
            src/**
      - name: Run tests
        if: steps.changed-files.outputs.any_changed == 'true'
        run: make test
```
For workflows with both `push` (path-filtered) and `pull_request` triggers, gate the changed-files step and subsequent steps with `github.event_name == 'pull_request'` / `github.event_name != 'pull_request' || steps.changed-files.outputs.any_changed == 'true'`.

## Downstream (Konflux) Build

The downstream build uses Konflux with hermetic builds (no network access during `docker build`). The downstream Containerfile is at `.rhdh/docker/Containerfile` and uses cachi2 gomod prefetch for Go module dependencies and rpm prefetch for system packages. All dependencies are Go modules — no vendored binaries, pip packages, or external tools are needed.

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
| `HEAP_DUMP_TIMEOUT` | `600` | Timeout for heap dump collection (seconds) |
| `HEAP_DUMP_REMOTE_DIR` | `/tmp` | Directory in container where heap dumps are written |
