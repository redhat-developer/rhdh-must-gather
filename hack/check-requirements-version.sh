#!/usr/bin/env bash
set -euo pipefail

MAKEFILE="Makefile"
REQ_IN=".rhdh/docker/requirements.in"

makefile_version=$(grep '^YQ_VERSION' "$MAKEFILE" | sed 's/.*:= *//')
req_in_version=$(grep '^yq==' "$REQ_IN" | sed 's/yq==//')

if [ "$makefile_version" = "$req_in_version" ]; then
    exit 0
fi

echo "YQ version mismatch: Makefile has $makefile_version, $REQ_IN has $req_in_version"
echo "Updating $REQ_IN to yq==$makefile_version"

sed -i.bak "s/^yq==.*/yq==$makefile_version/" "$REQ_IN" && rm -f "$REQ_IN.bak"
exit 1
