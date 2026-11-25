#!/usr/bin/env python3
"""Install or render the pinned cert-manager chart on Kind."""

import argparse
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time


REGISTRY = r"(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(?::[0-9]+)?"
REPOSITORY_COMPONENT = r"[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*"
OCI_CHART = re.compile(rf"oci://{REGISTRY}(?:/{REPOSITORY_COMPONENT})+")
SEMVER_NUMBER = r"(?:0|[1-9][0-9]*)"
PRERELEASE_ID = rf"(?:{SEMVER_NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
SEMVER = re.compile(
    rf"v?{SEMVER_NUMBER}\.{SEMVER_NUMBER}\.{SEMVER_NUMBER}"
    rf"(?:-{PRERELEASE_ID}(?:\.{PRERELEASE_ID})*)?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
)


class HelperArgumentParser(argparse.ArgumentParser):
    def error(self, _message):
        # argparse's normal diagnostics can quote untrusted arguments.
        self.exit(2, "ERROR: invalid or missing cert-manager helper arguments. See --help.\n")


def parse_args(argv):
    parser = HelperArgumentParser(
        prog="install-kind-cert-manager.py",
        description=__doc__,
        usage=("%(prog)s [--check-config | --template] --chart REF --version VERSION "
               "--pull-timeout SECONDS --pull-backoff DELAYS [-- HELM_ARGS ...]"),
        epilog="Pass Helm options after --.",
        allow_abbrev=False,
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check-config", action="store_true", help="Validate settings without running Helm")
    mode.add_argument("--template", action="store_true", help="Render instead of installing")
    parser.add_argument("--chart", required=True, metavar="REF", help="Untagged OCI chart reference")
    parser.add_argument("--version", required=True, help="Exact semantic version")
    parser.add_argument("--pull-timeout", required=True, metavar="SECONDS", help="Per-pull timeout (1 to 600 seconds)")
    parser.add_argument("--pull-backoff", required=True, metavar="DELAYS",
                        help="Up to five space-separated delays of 0 to 60 seconds each. Empty for one pull")

    boundary = argv.index("--") if "--" in argv else len(argv)
    config = parser.parse_args(argv[:boundary])
    helm_args = argv[boundary + 1:]
    if config.check_config and helm_args:
        parser.error("--check-config takes no Helm options")
    return config, helm_args


def validate_config(config):
    chart = config.chart
    version = config.version
    timeout = config.pull_timeout
    backoff = config.pull_backoff.split()

    if not OCI_CHART.fullmatch(chart):
        raise ValueError("KIND_CERT_MANAGER_CHART must be an untagged oci:// chart reference")
    # An OCI tag is at most 128 characters. Require a complete SemVer pin.
    if len(version) > 128 or not SEMVER.fullmatch(version):
        raise ValueError("KIND_CERT_MANAGER_VERSION must be an exact semantic version")
    if not re.fullmatch(r"[0-9]{1,3}", timeout) or not 1 <= int(timeout) <= 600:
        raise ValueError("KIND_CERT_MANAGER_PULL_TIMEOUT must be an integer from 1 to 600 seconds")
    if len(backoff) > 5 or any(
        not re.fullmatch(r"[0-9]{1,2}", delay) or int(delay) > 60 for delay in backoff
    ):
        raise ValueError(
            "KIND_CERT_MANAGER_PULL_BACKOFF must contain at most 5 space-separated "
            "integers from 0 to 60 seconds"
        )
    return chart, version, int(timeout), tuple(map(int, backoff))


def render_chart(chart, version, args):
    # Helm template can include registry redirects in its diagnostics as well.
    render = subprocess.run(
        ["helm", "template", "cert-manager", chart, "--version", version, *args],
        capture_output=True,
    )
    if render.returncode != 0:
        print(f"ERROR: cert-manager chart render failed (Helm exit {render.returncode})", file=sys.stderr)
    else:
        sys.stdout.buffer.write(render.stdout)
    return render.returncode


def install_chart(chart, version, timeout, backoff, args):
    attempts = len(backoff) + 1
    for attempt in range(1, attempts + 1):
        # A failed pull can leave a partial file. Each attempt owns its directory.
        with tempfile.TemporaryDirectory(prefix="osac-cert-manager-") as directory:
            try:
                pull = subprocess.run(
                    ["helm", "pull", chart, "--version", version, "--destination", directory],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.PIPE,
                    timeout=timeout,
                )
            except subprocess.TimeoutExpired:
                print(f"ERROR: cert-manager chart download exceeded {timeout}s", file=sys.stderr)
                return 124

            if pull.returncode == 0:
                # Helm determines the archive name from the OCI reference and tag.
                packages = list(Path(directory).glob("*.tgz"))
                if len(packages) != 1 or not stat.S_ISREG(packages[0].lstat().st_mode):
                    print("ERROR: Helm must produce exactly one regular cert-manager chart archive", file=sys.stderr)
                    return 1
                # Only acquisition is retried. Helm can change cluster state on failure.
                return subprocess.run(
                    ["helm", "upgrade", "--install", "cert-manager", str(packages[0]),
                     "--version", version, *args],
                ).returncode

        # Registry responses can include signed URLs. Report only fixed reasons.
        error = pull.stderr.lower().rstrip()
        if re.search(rb"\b(?:unauthorized|forbidden|authentication required|access denied)\b", error):
            reason = "registry authentication or authorization failed"
        elif re.search(rb"\b(?:not found|manifest unknown|no matching version)\b", error):
            reason = "chart or version not found"
        elif re.search(rb"(?:^|: )connection reset by peer$", error):
            reason = "connection reset by peer"
            if attempt < attempts:
                delay = backoff[attempt - 1]
                print(
                    f"cert-manager chart download attempt {attempt}/{attempts}: "
                    f"{reason}; retrying in {delay}s",
                    file=sys.stderr,
                )
                time.sleep(delay)
                continue
        else:
            reason = "unexpected Helm error"

        print(
            f"ERROR: cert-manager chart download failed on attempt {attempt}/{attempts}: "
            f"{reason} (Helm exit {pull.returncode})",
            file=sys.stderr,
        )
        return pull.returncode


def main():
    config, helm_args = parse_args(sys.argv[1:])
    try:
        chart, version, timeout, backoff = validate_config(config)
    except ValueError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2

    if config.check_config:
        return 0
    if config.template:
        return render_chart(chart, version, helm_args)
    return install_chart(chart, version, timeout, backoff, helm_args)


if __name__ == "__main__":
    status = main()
    sys.exit(status if status >= 0 else 128 - status)
