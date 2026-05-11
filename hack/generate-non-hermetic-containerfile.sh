#!/bin/bash
#
# Generates a non-hermetic Containerfile from the hermetic one by stripping
# Cachi2/Hermeto-specific constructs. The resulting Containerfile can be built
# with normal network access (no dependency prefetching required).
#
# Usage: ./hack/generate-non-hermetic-containerfile.sh [input] [output]
#   input:  path to the hermetic Containerfile (default: ./Containerfile)
#   output: path for the generated file (default: ./Containerfile.non-hermetic)
set -euo pipefail

INPUT="${1:-./Containerfile}"
OUTPUT="${2:-./Containerfile.non-hermetic}"

if [[ ! -f "${INPUT}" ]]; then
  echo "Error: input file '${INPUT}' not found" >&2
  exit 1
fi

# Check for GNU sed on macOS
if [[ "$OSTYPE" == "darwin"* ]]; then
  if ! sed --version 2>/dev/null | grep -q "GNU sed"; then
    echo "Error: GNU sed is required on macOS." >&2
    echo "Install it with: brew install gnu-sed" >&2
    echo "Then add to your PATH: export PATH=\"\$(brew --prefix)/opt/gnu-sed/libexec/gnubin:\$PATH\"" >&2
    exit 1
  fi
fi

# Use awk for precise multi-line block removal, then sed for simple substitutions.
awk '
# Skip lines containing ". /cachi2/cachi2.env"
/\. \/cachi2\/cachi2\.env/ { next }

# Skip the RPM repo setup blocks: from "arch=$(uname -m)" through "test -n...yum.repos.d"
/arch=\$\(uname -m\)/ { in_rpm_block = 1 }
in_rpm_block {
  if (/test -n/) {
    in_rpm_block = 0
  }
  next
}

# Print everything else
{ print }
' "${INPUT}" > "${OUTPUT}"

# Update the header comment
sed -i '1s/.*/# Auto-generated non-hermetic Containerfile — do not edit. Source: Containerfile/' "${OUTPUT}"

# Remove --locked from cargo build (vendored cargo registry not available without cachi2)
sed -i 's/ --locked//' "${OUTPUT}"

# Strip --require-hashes, --no-deps, --no-build-isolation from pip install
sed -i 's/ --no-deps//g' "${OUTPUT}"
sed -i 's/ --require-hashes//g' "${OUTPUT}"
sed -i 's/ --no-build-isolation//g' "${OUTPUT}"

# Remove 'openshift-clients' and 'helm' from microdnf install (not in default UBI repos)
sed -i '/^      openshift-clients \\$/d' "${OUTPUT}"
sed -i '/^      helm \\$/d' "${OUTPUT}"

# Add curl-based oc + helm installs before the COPY requirements-build.txt line
sed -i '/^COPY requirements-build\.txt/i \
# Install oc (OpenShift CLI)\
RUN curl -fsSL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable-4.18/openshift-client-linux.tar.gz \\\
    | tar xz -C /usr/local/bin/ oc kubectl \\\
    && chmod +x /usr/local/bin/oc /usr/local/bin/kubectl\
\
# Install Helm\
RUN curl -fsSL "https://get.helm.sh/helm-v3.17.3-linux-$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/").tar.gz" -o /tmp/helm.tar.gz \\\
    && tar xzf /tmp/helm.tar.gz -C /tmp/ \\\
    && mv /tmp/linux-*/helm /usr/local/bin/helm \\\
    && rm -rf /tmp/helm.tar.gz /tmp/linux-*\
' "${OUTPUT}"

echo "Generated non-hermetic Containerfile: ${OUTPUT}"
