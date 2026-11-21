#!/usr/bin/env python3
"""Contract regression for the Netris networking provisioning input.

The ``prep-osac`` role of osac-project/osac-test-infra turns the
``networking_provisioning_enabled`` inventory/EXTRA_VARS value into
``values/<profile>/netris-runtime.yaml``, which ``install-osac`` layers after
``values/<profile>/instance.yaml`` and ``values/<profile>/netris.yaml``.

This script covers the whole path: it runs the role's own overlay builder and
YAML writer (``infra/netris/roles/prep-osac/tasks/generate-values.yml``) through
Ansible with dummy lab facts for each inventory input, then feeds the file that
run produced into the Helm checks. That asserts the generated runtime value is a
YAML boolean, that it wins over the profile overlays, and that every enabled
consumer receives it, including BMF's ``OSAC_ENABLE_NETWORKING_PROVISIONING``.

Requirements, which fail rather than skip: an ``osac-test-infra`` checkout
(``OSAC_TEST_INFRA``, default ``../osac-test-infra``; ``tools/bootstrap.sh``
creates it) and ``ansible-playbook`` on PATH (``ansible-core``).
"""

from __future__ import annotations

import functools
import itertools
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest
from typing import Any

import yaml

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
INSTALLER = SCRIPT_DIR.parent
REPO = INSTALLER.parent
CHART = INSTALLER / "charts/osac"
HARNESS = SCRIPT_DIR / "testdata/netris-runtime/produce-overlay.yml"
HELM = os.environ.get("HELM_BIN", "helm")

INVENTORY_FILE = pathlib.Path("infra/netris/inventory/group_vars/all.yml")
ROLE_TASKS = pathlib.Path("infra/netris/roles/prep-osac/tasks/generate-values.yml")
OVERLAY_NAME = "netris-runtime.yaml"

# (case, value passed the way inventory/EXTRA_VARS supplies it, None = the
# inventory default, expected global.networking.provisioningEnabled). The
# expectation is spelled out and independent of the producer.
INPUT_CASES: tuple[tuple[str, bool | None, bool], ...] = (
    ("omitted", None, True),
    ("explicit-true", True, True),
    ("json-false", False, False),
)

# Profiles that deploy the BMF subchart (global.services.bmaas.enabled) and one
# that does not, so the BMF assertion cannot pass vacuously.
BMF_PROFILES = ("full-ci", "bmaas-ci")
NO_BMF_PROFILE = "vmaas-ci"

# Values only the lab knows. The role's overlay carries the networking contract.
LAB_OVERRIDES = {
    "global": {"clusterDomain": "example.com"},
    "service": {"externalHostname": "api.example.com", "internalHostname": "internal-api.example.com"},
    "ui": {"auth": {"oidcIssuerURL": "https://keycloak.example.com/realms/osac"}},
}

_TMP = pathlib.Path(tempfile.mkdtemp(prefix="osac-netris-overlay-"))
LAB_VALUES = _TMP / "lab.json"
LAB_VALUES.write_text(json.dumps(LAB_OVERRIDES))
_checkout: pathlib.Path | None = None


def test_infra_checkout() -> pathlib.Path:
    """The osac-test-infra checkout holding the producer, or a hard failure."""
    global _checkout
    if _checkout is None:
        configured = os.environ.get("OSAC_TEST_INFRA")
        checkout = pathlib.Path(configured) if configured else REPO.parent / "osac-test-infra"
        missing = [path for path in (INVENTORY_FILE, ROLE_TASKS) if not (checkout / path).is_file()]
        if missing:
            raise AssertionError(
                f"osac-test-infra checkout at {checkout} lacks {', '.join(map(str, missing))}; "
                "run tools/bootstrap.sh or set OSAC_TEST_INFRA to a checkout that has the "
                "prep-osac values task file this regression drives"
            )
        _checkout = checkout
    return _checkout


def inventory_default() -> bool:
    """The default the inventory declares for networking_provisioning_enabled."""
    text = (test_infra_checkout() / INVENTORY_FILE).read_text()
    match = [line for line in text.splitlines() if line.startswith("networking_provisioning_enabled:")]
    if not match:
        raise AssertionError(f"{INVENTORY_FILE} no longer declares networking_provisioning_enabled")
    return yaml.safe_load(match[0].split(":", 1)[1].strip())


@functools.lru_cache(maxsize=None)
def write_overlay(case: str, inventory_input: bool | None) -> pathlib.Path:
    """Run the role's values tasks and return the netris-runtime.yaml they wrote."""
    checkout = test_infra_checkout()
    playbook = shutil.which("ansible-playbook")
    if playbook is None:
        raise AssertionError(
            "ansible-playbook is not on PATH; install ansible-core or run "
            "`uv run --with ansible-core --project .. python scripts/test-networking-provisioning-overlay.py`"
        )
    value = inventory_default() if inventory_input is None else inventory_input
    output = _TMP / f"runtime-{case}"
    output.mkdir(parents=True, exist_ok=True)
    extra_vars = _TMP / f"input-{case}.json"
    # inventory/EXTRA_VARS carries the value as JSON; the omitted case supplies
    # the default the inventory declares.
    extra_vars.write_text(json.dumps({"networking_provisioning_enabled": value}))
    environment = {
        **os.environ,
        "OSAC_TEST_INFRA": str(checkout),
        "ANSIBLE_ROLES_PATH": str(checkout / ROLE_TASKS.parents[2]),
        "ANSIBLE_NOCOLOR": "1",
        "ANSIBLE_FORCE_COLOR": "0",
    }
    result = subprocess.run(
        [playbook, "-i", "localhost,", "-c", "local", str(HARNESS),
         "-e", f"output_dir={output}", "-e", f"@{extra_vars}"],
        capture_output=True,
        text=True,
        env=environment,
    )
    overlay = output / OVERLAY_NAME
    if result.returncode != 0 or not overlay.is_file():
        raise AssertionError(
            f"the prep-osac values tasks did not write {OVERLAY_NAME} for {case}:\n"
            f"{result.stdout[-4000:]}\n{result.stderr[-2000:]}"
        )
    return overlay


def load(path: pathlib.Path) -> dict[str, Any]:
    return yaml.safe_load(path.read_text())


def helm(*value_files: pathlib.Path) -> subprocess.CompletedProcess:
    args = list(itertools.chain.from_iterable(("--values", str(path)) for path in value_files))
    return subprocess.run(
        [HELM, "template", "osac", str(CHART), "--namespace", "osac", *args],
        capture_output=True,
        text=True,
    )


def render(*value_files: pathlib.Path) -> list[dict[str, Any]]:
    result = helm(*value_files)
    if result.returncode != 0:
        raise AssertionError(f"render failed: {result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def install_render(profile: str, overlay: pathlib.Path) -> list[dict[str, Any]]:
    """Render the way install-osac does: profile, profile overlay, runtime overlay."""
    return render(
        INSTALLER / "values" / profile / "instance.yaml",
        INSTALLER / "values" / profile / "netris.yaml",
        LAB_VALUES,
        overlay,
    )


def deployments(docs: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [doc for doc in docs if doc.get("kind") == "Deployment"]


def env_of(doc: dict[str, Any]) -> dict[str, str]:
    values: dict[str, str] = {}
    for container in doc["spec"]["template"]["spec"]["containers"]:
        for entry in container.get("env") or []:
            values[entry["name"]] = entry.get("value")
    return values


def args_of(doc: dict[str, Any]) -> list[str]:
    args: list[str] = []
    for container in doc["spec"]["template"]["spec"]["containers"]:
        args.extend(container.get("args") or [])
        args.extend(container.get("command") or [])
    return args


def provisioning_args(docs: list[dict[str, Any]]) -> dict[str, str]:
    """The --networking-provisioning-enabled value every fulfillment deployment takes."""
    found: dict[str, str] = {}
    for doc in deployments(docs):
        for arg in args_of(doc):
            if arg.startswith("--networking-provisioning-enabled="):
                found[doc["metadata"]["name"]] = arg.split("=", 1)[1]
    return found


def operator_env(docs: list[dict[str, Any]]) -> dict[str, str]:
    [operator] = [doc for doc in deployments(docs) if doc["metadata"]["name"] == "osac-operator"]
    return env_of(operator)


def bmf_envs(docs: list[dict[str, Any]]) -> dict[str, str]:
    found: dict[str, str] = {}
    for doc in deployments(docs):
        value = env_of(doc).get("OSAC_ENABLE_NETWORKING_PROVISIONING")
        if value is not None:
            found[doc["metadata"]["name"]] = value
    return found


class NetrisRuntimeOverlayContractTest(unittest.TestCase):
    """The inventory input and the overlay the role writes for it."""

    def test_inventory_declares_the_true_default(self):
        self.assertIs(inventory_default(), True)

    def test_input_matrix_generates_a_yaml_boolean(self):
        for case, inventory_input, expected in INPUT_CASES:
            with self.subTest(case=case):
                generated = load(write_overlay(case, inventory_input))
                value = generated["global"]["networking"]["provisioningEnabled"]
                self.assertIs(type(value), bool, f"{case} writes {value!r}, not a YAML boolean")
                self.assertIs(value, expected)


class NetrisRuntimeOverlayPrecedenceTest(unittest.TestCase):
    """The runtime overlay is the last values layer, so it decides the setting."""

    def test_runtime_overlay_wins_over_the_profile_overlays(self):
        # The committed profiles disagree on purpose: instance.yaml disables
        # provider networking and each Netris overlay turns it back on.
        instance = load(INSTALLER / "values" / "full-ci" / "instance.yaml")
        netris = load(INSTALLER / "values" / "full-ci" / "netris.yaml")
        self.assertIs(instance["global"]["networking"]["provisioningEnabled"], False)
        self.assertIs(netris["global"]["networking"]["provisioningEnabled"], True)

        self.assertEqual(provisioning_args(install_render("full-ci", write_overlay("omitted", None))),
                         {"fulfillment-controller": "true", "fulfillment-grpc-server": "true"})
        self.assertEqual(provisioning_args(install_render("full-ci", write_overlay("json-false", False))),
                         {"fulfillment-controller": "false", "fulfillment-grpc-server": "false"})

        # Same files, overlay applied before the profile overlay: the profile
        # wins, so the assertions above depend on install-osac's value order.
        reordered = render(
            INSTALLER / "values" / "full-ci" / "instance.yaml",
            write_overlay("json-false", False),
            INSTALLER / "values" / "full-ci" / "netris.yaml",
            LAB_VALUES,
        )
        self.assertEqual(provisioning_args(reordered),
                         {"fulfillment-controller": "true", "fulfillment-grpc-server": "true"})


class NetrisProvisioningConsumersTest(unittest.TestCase):
    """Every enabled consumer receives the same selected value."""

    def assert_consumers(self, docs: list[dict[str, Any]], expected: bool, bmf: bool):
        value = str(expected).lower()
        self.assertEqual(
            provisioning_args(docs),
            {"fulfillment-controller": value, "fulfillment-grpc-server": value},
        )
        self.assertEqual(operator_env(docs)["OSAC_PROVISIONING_ENABLED"], value)
        self.assertEqual(bmf_envs(docs), {"bmf-operator-controller-manager": value} if bmf else {})

    def test_overlay_value_reaches_every_consumer(self):
        for profile in (*BMF_PROFILES, NO_BMF_PROFILE):
            bmf = profile in BMF_PROFILES
            for case, inventory_input, expected in INPUT_CASES:
                with self.subTest(profile=profile, case=case):
                    overlay = write_overlay(case, inventory_input)
                    self.assert_consumers(install_render(profile, overlay), expected, bmf)


if __name__ == "__main__":
    unittest.main()
