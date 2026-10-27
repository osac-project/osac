"""Shared helpers for the profile render-check scripts.

render() templates the platform chart for one profile the way the Makefile's
render-check targets do, and select_one() picks the single rendered document
a check addresses.
"""

from __future__ import annotations

import pathlib
import subprocess
from collections.abc import Callable

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
PROFILES = ("vmaas-ci", "bmaas-ci", "caas-ci", "full-ci", "cudn-evpn-netris-test")


def template_command(profile: str) -> list[str]:
    """The helm template command render() runs for one profile.

    Exposed so checks can append their own arguments and observe whether Helm
    accepted the values, which render() hides behind its error handling.
    """
    return [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--values",
        str(INSTALLER / "values" / profile / "instance.yaml"),
        "--set-string",
        "global.clusterDomain=example.com",
        "--set",
        "service.externalHostname=fulfillment.example.com",
        "--set",
        "service.internalHostname=fulfillment-internal.example.com",
    ]


def render(profile: str) -> list[str]:
    try:
        manifest = subprocess.run(
            template_command(profile), check=True, capture_output=True, text=True, timeout=300
        ).stdout
    except subprocess.TimeoutExpired as exc:
        raise SystemExit(
            f"helm template timed out after {exc.timeout} seconds for profile {profile}"
        ) from exc
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def select_one(docs: list[str], predicate: Callable[[str], bool], description: str) -> str:
    matches = [doc for doc in docs if predicate(doc)]
    if len(matches) != 1:
        raise AssertionError(f"expected one {description}, found {len(matches)}")
    return matches[0]
