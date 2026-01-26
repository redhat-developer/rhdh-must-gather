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

**Important**: While automatic sanitization catches common sensitive patterns, always review the sanitization report and manually check for any domain-specific sensitive information before sharing externally.
