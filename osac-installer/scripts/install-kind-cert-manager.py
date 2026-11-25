#!/usr/bin/env python3
"""Fetch the pinned cert-manager chart and install it once on Kind."""

from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time


CHART = "oci://quay.io/jetstack/charts/cert-manager"
VERSION = "v1.20.0"
PULL_TIMEOUT = 90
BACKOFF = (2, 5)


def pull_chart(package):
    attempts = len(BACKOFF) + 1
    for attempt in range(1, attempts + 1):
        try:
            pull = subprocess.run(
                ["helm", "pull", CHART, "--version", VERSION, "--destination", str(package.parent)],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                timeout=PULL_TIMEOUT,
            )
        except subprocess.TimeoutExpired:
            print(f"ERROR: cert-manager {VERSION} download exceeded {PULL_TIMEOUT}s", file=sys.stderr)
            return 124

        if pull.returncode == 0:
            if package.is_file():
                return 0
            print(f"ERROR: Helm did not create {package.name}", file=sys.stderr)
            return 1

        # Registry responses can include signed URLs. Report only fixed reasons.
        error = pull.stderr.lower().rstrip()
        if re.search(rb"\b(?:unauthorized|forbidden|authentication required|access denied)\b", error):
            reason = "registry authentication or authorization failed"
        elif re.search(rb"\b(?:not found|manifest unknown|no matching version)\b", error):
            reason = "chart or version not found"
        elif re.search(rb"(?:^|: )connection reset by peer$", error):
            reason = "connection reset by peer"
            if attempt < attempts:
                delay = BACKOFF[attempt - 1]
                print(
                    f"cert-manager {VERSION} download attempt {attempt}/{attempts}: "
                    f"{reason}; retrying in {delay}s",
                    file=sys.stderr,
                )
                package.unlink(missing_ok=True)
                time.sleep(delay)
                continue
        else:
            reason = "unexpected Helm error"

        print(
            f"ERROR: cert-manager {VERSION} download failed on attempt {attempt}/{attempts}: "
            f"{reason} (Helm exit {pull.returncode})",
            file=sys.stderr,
        )
        return pull.returncode


def main():
    with tempfile.TemporaryDirectory(prefix="osac-cert-manager-") as directory:
        # Helm uses the literal OCI tag in the package name, including the v.
        package = Path(directory) / f"cert-manager-{VERSION}.tgz"
        status = pull_chart(package)
        if status != 0:
            return status
        # Only acquisition is retried. Helm can change cluster state on failure.
        return subprocess.run(
            ["helm", "upgrade", "--install", "cert-manager", str(package), "--version", VERSION, *sys.argv[1:]]
        ).returncode


if __name__ == "__main__":
    status = main()
    sys.exit(status if status >= 0 else 128 - status)
