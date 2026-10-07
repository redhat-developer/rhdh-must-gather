## Privacy and Security

### Secret Collection (Opt-In by Default)

**By default, Kubernetes Secrets are NOT collected** to enhance privacy and security. To collect secrets (which will be automatically sanitized), use the `--with-secrets` flag:

```bash
# Default: secrets excluded
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather

# Opt-in: include secrets (will be sanitized)
oc adm must-gather --image=quay.io/rhdh-community/rhdh-must-gather -- /usr/bin/gather --with-secrets
```

When secrets are excluded (default behavior):
- Secret resources are removed from Namespace inspect data
- Secret resources are filtered from Helm manifests
- Secret collection is skipped in helm/operator data gathering
- ConfigMaps and other resources are still collected normally

When secrets are included (`--with-secrets`):
- Secrets are collected from all sources
- All secret data values are automatically sanitized (see below)
- Secret metadata (names, labels, annotations) is preserved for diagnostic purposes

### Automatic Data Sanitization

When secrets are collected (`--with-secrets`), the tool includes automatic sanitization of sensitive information to make the collected data safe for sharing. **All collected data is sanitized**, including:

**Data Sources Sanitized:**
- **Helm release data** - ConfigMaps, Secrets, and deployed manifests
- **Operator resources** - Backstage CRs, operator configs, and secrets
- **Namespace inspect data** - All resources collected by `oc adm inspect` (Secrets, ConfigMaps, pod specs, etc.)
- **Platform information** - System and cluster metadata
- **Log files** - Container logs and must-gather execution logs

**Automatically Sanitized Sensitive Content:**
- **Kubernetes Secret data values** - All `data:` fields in Secret resources (including nested/indented Secrets from `oc adm inspect` output) are replaced with `[REDACTED]`
- **Base64 encoded sensitive data** - Long base64 strings (40+ characters) that likely contain tokens, passwords, or certificates
- **JWT tokens** - Complete JWT tokens matching the standard format (`eyXXX.eyXXX.XXX`)
- **Bearer tokens** - Authorization headers with bearer tokens
- **SSH private keys and TLS certificates** - Complete key blocks from BEGIN to END
- **Database connection strings** - PostgreSQL and other DB URLs containing embedded credentials
- **OAuth tokens and API keys** - Authentication tokens and client secrets
- **URLs with credentials** - HTTP/HTTPS URLs with username:password@ format

**Sanitization Features:**
- **Precision targeting** - Avoids false positives on legitimate data like Kubernetes status fields
- **Structure preservation** - Maintains YAML/JSON structure for diagnostic value
- **Comprehensive coverage** - Processes all YAML, JSON, and text files in the collected data
- **Detailed reporting** - Provides sanitization summary with file and item counts

### Automatic obfuscation

After secret sanitization, the gather runs [must-gather-clean](https://github.com/openshift/must-gather-clean) on the collected tree. This step is on by default. It rewrites:

- IP addresses, in file contents and in file paths, using one consistent placeholder per address (`127.0.0.1`, `0.0.0.0`, and `::1` are left as-is)
- MAC addresses, the same way
- Cluster domain names, when they can be discovered. The name in front of the domain is kept (`console.apps.example.com` becomes `console.apps.domain0000000001`) so the gather is still readable

On OpenShift, domain discovery reads the DNS `cluster` base domain, the default ingress controller domain, and the API server hostname. On Kubernetes, and whenever those OpenShift domains cannot be read, discovery uses host names from Ingress resources and OpenShift Routes in the namespaces being collected, plus the API server hostname. In-cluster names such as `cluster.local` and `kubernetes.default.svc` are not treated as customer domains. When none of those names can be read, IP and MAC obfuscation still run, and other hostnames are left unchanged. Set `RHDH_OBFUSCATE_DOMAINS` to a comma-separated list to add domains discovery missed.

ConfigMaps and Secrets are not removed by this step. Secret values are still redacted by the sanitizer above, and secret names stay in the gather when `--with-secrets` was used.

The reversible `report.yaml` map produced by must-gather-clean is not included in the output. Do not copy it into a gather you share. A `watermark.txt` file in the output records that obfuscation ran.

Obfuscation always runs as part of the must-gather workflow. If obfuscation fails for any reason, the command will log a warning and continue, returning the collected data with a notice to review it carefully before sharing with support. This ensures that must-gather can always complete successfully even if obfuscation encounters issues.

**Important**: While automatic sanitization and obfuscation catch common sensitive patterns and cover discovered domains, IPs, and MAC addresses, always review the must-gather output and check for any domain-specific sensitive information before sharing externally.
