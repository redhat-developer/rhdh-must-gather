#!/usr/bin/env python3
"""Strip source-distribution (sdist) hashes from a pip requirements file.

Konflux hermetic builds (Cachi2/Hermeto) prefetch Python packages based on
the hashes in requirements.txt. If sdist hashes are present, Cachi2 may
download source tarballs whose build backends (hatchling, poetry-core, etc.)
are NOT prefetched, breaking the network-isolated build.

Removing sdist hashes forces Cachi2 to fetch only wheels.

Usage:  hack/strip-sdist-hashes.py .rhdh/docker/requirements.txt
"""

import json
import re
import sys
import urllib.request

HASH_RE = re.compile(r"^\s+--hash=sha256:([a-f0-9]+)")
PKG_RE = re.compile(r"^([A-Za-z0-9_.-]+)==(\S+)", re.M)


def get_sdist_sha256s(name, version):
    url = f"https://pypi.org/pypi/{name}/{version}/json"
    try:
        with urllib.request.urlopen(url, timeout=15) as r:
            data = json.loads(r.read())
    except Exception:
        return set()
    return {
        f["digests"]["sha256"]
        for f in data.get("urls", [])
        if f["packagetype"] == "sdist" and "sha256" in f.get("digests", {})
    }


def main():
    if len(sys.argv) < 2:
        print(f"usage: {sys.argv[0]} REQUIREMENTS_TXT", file=sys.stderr)
        sys.exit(2)

    path = sys.argv[1]
    with open(path) as fh:
        text = fh.read()

    to_strip = set()
    for m in PKG_RE.finditer(text):
        pkg, ver = m.group(1), m.group(2).rstrip(" \\")
        to_strip |= get_sdist_sha256s(pkg, ver)

    if not to_strip:
        return

    # Drop lines whose hash is in the sdist set
    out = []
    for line in text.splitlines(keepends=True):
        m = HASH_RE.match(line)
        if m and m.group(1) in to_strip:
            continue
        out.append(line)

    # Fix dangling backslashes left when the removed hash was followed by
    # a non-hash line (comment, next package, blank line, EOF).
    result = []
    for i, line in enumerate(out):
        s = line.rstrip("\n")
        if s.endswith(" \\"):
            j = i + 1
            while j < len(out) and out[j].strip() == "":
                j += 1
            if j >= len(out) or not HASH_RE.match(out[j]):
                line = s[:-2].rstrip() + "\n"
        result.append(line)

    new_text = "".join(result)
    if new_text != text:
        with open(path, "w") as fh:
            fh.write(new_text)
        print(f"Stripped {len(to_strip)} sdist hash(es) from {path}")
        sys.exit(1)


if __name__ == "__main__":
    main()
