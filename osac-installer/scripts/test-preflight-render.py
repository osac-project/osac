#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Offline Helm render checks for the install preflight work.

Everything here runs `helm template` with no cluster. Checks that need a live
API server live in test-preflight-kind.py; checks that mock the cluster live in
the helm-unittest suites under charts/osac/tests/.

Run: uv run --script scripts/test-preflight-render.py
"""

from __future__ import annotations

import json
import pathlib
import re
import subprocess
import tarfile
import tempfile

import jsonschema
import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
CSI_CHART = INSTALLER.parent / "osac-csi-driver/charts/csi-driver"

# Hook Jobs of the osac release. Rendering needs the feature flags below, the
# same ones test-fulfillment-trust-render.py uses to bring every hook in.
OSAC_HOOK_JOBS = {
    "osac-db-init",
    "osac-publish-templates",
    "fulfillment-create-hub",
    "create-network-class",
    "register-local-storage",
    "seed-cluster-versions",
    "osac-aap-bootstrap",
    "osac-aap-create-token",
}
# Replaced by the render-time preflight engine; it must not come back.
RETIRED_HOOK_JOBS = {"osac-pre-install-validate"}
# Both Jobs must end before Helm's own 40m --timeout from the install guide.
CSI_DEADLINES = {
    "csi-driver-fulfillment-trust-bootstrap": 1800,
    "csi-driver-fulfillment-trust-cleanup": 600,
}
HELM_WAIT_SECONDS = 40 * 60
CATALOG = CHART / "files/preflight/catalog.yaml"
SCHEMA = CHART / "files/preflight/report.schema.json"
ENGINE = CHART / "templates/preflight/_engine.tpl"


def helm_template(*args: str) -> list[str]:
    manifest = subprocess.run(
        ["helm", "template", *args], check=True, capture_output=True, text=True
    ).stdout
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def render_osac(*extra: str, values: str = "ci/default-values.yaml") -> list[str]:
    return helm_template(
        "osac",
        str(CHART),
        "--values",
        str(CHART / values),
        *extra,
        "--set",
        "global.osacDeploymentId=ci/osac",
        "--set-string",
        "global.clusterDomain=example.com",
        "--set",
        "lvms.enabled=true",
        "--set",
        "hubAccess.enabled=true",
        "--set",
        "clusterVersions.enabled=true",
        "--set",
        "aap.instanceGroups.publishTemplates.enabled=true",
    )


def render_csi() -> list[str]:
    return helm_template(
        "csi-driver",
        str(CSI_CHART),
        "--set",
        "namespace=osac-csi",
        "--set",
        "global.fulfillmentTrust.enabled=true",
        "--set",
        "global.fulfillmentTrust.clusterOrderUID=ci-order",
        "--set",
        "global.fulfillmentTrust.tenant=ci-tenant",
        "--set",
        "global.fulfillmentTrust.admission.image.tag=v0.0.0-ci",
        "--set",
        "controller.fulfillment.configMap.name=osac-csi-fulfillment-config",
        "--set",
        "controller.fulfillment.credentials.secretName=osac-csi-driver-credentials",
        "--set",
        "controller.fulfillment.allowStub=false",
    )


def hook_jobs(docs: list[str]) -> dict[str, str]:
    """Return the rendered Job documents that are Helm hooks, by name."""
    jobs = {}
    for doc in docs:
        if not re.search(r"(?m)^kind: Job$", doc):
            continue
        if not re.search(r'(?m)^    "helm\.sh/hook": ', doc):
            continue
        name = re.search(r"(?m)^  name: (\S+)$", doc)
        assert name, f"hook Job without a name:\n{doc}"
        jobs[name.group(1).strip('"')] = doc
    return jobs


def has_log_policy(job: str) -> bool:
    return bool(
        re.search(r'(?m)^    "helm\.sh/hook-output-log-policy": hook-failed$', job)
    )


def check_hook_log_policy() -> None:
    """A failed hook must print its pod log; Helm only does so with the policy."""
    osac = hook_jobs(render_osac())
    missing = OSAC_HOOK_JOBS - osac.keys()
    assert not missing, f"expected hook Jobs not rendered: {sorted(missing)}"
    assert not RETIRED_HOOK_JOBS & osac.keys(), "shell pre-install hook is rendered again"
    for name, job in osac.items():
        assert has_log_policy(job), f"{name}: missing hook-output-log-policy: hook-failed"

    csi = hook_jobs(render_csi())
    assert csi.keys() == CSI_DEADLINES.keys(), f"unexpected CSI hook Jobs: {sorted(csi)}"
    for name, job in csi.items():
        assert has_log_policy(job), f"{name}: missing hook-output-log-policy: hook-failed"


def check_csi_hook_deadlines() -> None:
    """A stuck CSI hook must fail on its own before Helm's --timeout expires."""
    for name, job in hook_jobs(render_csi()).items():
        match = re.search(r"(?m)^  activeDeadlineSeconds: (\d+)$", job)
        assert match, f"{name}: no activeDeadlineSeconds"
        deadline = int(match.group(1))
        assert deadline == CSI_DEADLINES[name], f"{name}: deadline {deadline}"
        assert deadline < HELM_WAIT_SECONDS, f"{name}: deadline not below Helm's wait"


def offline_report(*extra: str, values: str = "ci/default-values.yaml") -> dict:
    """The report ConfigMap of an offline render, parsed."""
    for doc in render_osac(*extra, values=values):
        parsed = yaml.safe_load(doc)
        if parsed and parsed["kind"] == "ConfigMap" and parsed["metadata"]["name"].endswith("-preflight-report"):
            return json.loads(parsed["data"]["render.json"])
    raise AssertionError("no preflight report ConfigMap rendered")


def check_report_schema() -> None:
    """The report validates against the published schema, offline and with checks skipped."""
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    jsonschema.Draft202012Validator.check_schema(schema)
    validator = jsonschema.Draft202012Validator(schema, format_checker=jsonschema.FormatChecker())
    for extra in ((), ("--set", "validation.enabled=false"), ("--set", "preflight.mode=strict")):
        validator.validate(offline_report(*extra))
    for values in sorted(p.name for p in (CHART / "ci").glob("*-values.yaml")):
        validator.validate(offline_report(values=f"ci/{values}"))


def check_offline_never_blocks() -> None:
    """A render with no cluster access must not block: helm template and GitOps keep working."""
    report = offline_report()
    assert report["summary"]["blocking"] == 0, report["summary"]
    assert report["kubeVersion"] is None, "an offline render must not invent a Kubernetes version"
    assert report["generatedAt"].endswith("Z"), report["generatedAt"]
    for result in report["results"]:
        assert result["status"] in ("unknown", "skipped"), (result["id"], result["status"])


def check_catalog_matches_engine_and_schema() -> None:
    """Every catalog entry has a handler, valid references and values the schema allows."""
    catalog = yaml.safe_load(CATALOG.read_text(encoding="utf-8"))
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = schema["$defs"]["result"]["properties"]
    ids = [c["id"] for c in catalog]
    assert len(ids) == len(set(ids)), "duplicate check id"
    engine = ENGINE.read_text(encoding="utf-8")
    for check in catalog:
        assert f'define "osac.preflight.kind.{check["kind"]}"' in engine, f'{check["id"]}: no handler for {check["kind"]}'
        assert check["severity"] in result["severity"]["enum"], check["id"]
        assert check.get("severityUpgrade", check["severity"]) in result["severity"]["enum"], check["id"]
        assert check["category"] in result["category"]["enum"], check["id"]
        assert set(check["requiredFor"]) <= set(result["requiredFor"]["items"]["enum"]), check["id"]
        assert set(check.get("dependsOn", [])) <= set(ids), f'{check["id"]}: dependsOn names an unknown check'
        for field in ("title", "remediation", "docsURL"):
            assert check.get(field), f'{check["id"]}: missing {field}'
    offline = {r["id"] for r in offline_report()["results"]}
    assert offline == set(ids), f"report ids differ from the catalog: {offline ^ set(ids)}"


def check_values_are_closed() -> None:
    """preflight.* values are constrained by the chart schema, as the installer rules require."""
    for bad in ("preflight.mode=bogus", "preflight.skip.cert-issuer-ready=", "preflight.unknown=1"):
        proc = subprocess.run(
            ["helm", "template", "osac", str(CHART), "--values", str(CHART / "ci/default-values.yaml"), "--set", bad],
            capture_output=True,
            text=True,
        )
        assert proc.returncode != 0, f"{bad} was accepted"
        assert "values don't meet the specifications of the schema" in proc.stderr, (bad, proc.stderr)


PACKAGED = (
    "osac/files/preflight/catalog.yaml",
    "osac/files/preflight/report.schema.json",
    "osac/templates/preflight/_engine.tpl",
    "osac/templates/preflight/report.yaml",
)


def check_chart_archive_carries_the_preflight() -> None:
    """Clients install the packaged chart. A packaging rule must not drop the gate, and tests must not ship."""
    with tempfile.TemporaryDirectory() as out:
        subprocess.run(["helm", "package", str(CHART), "-d", out], check=True, capture_output=True, text=True)
        archive = next(pathlib.Path(out).glob("osac-*.tgz"))
        with tarfile.open(archive) as tar:
            names = tar.getnames()
        missing = [f for f in PACKAGED if f not in names]
        assert not missing, f"missing from the chart archive: {missing}"
        shipped = [n for n in names if n.startswith("osac/tests/")]
        assert not shipped, f"unit tests are packaged: {shipped[:3]}"
        manifest = subprocess.run(
            ["helm", "template", "osac", str(archive), "--values", str(CHART / "ci/default-values.yaml"),
             "-s", "templates/preflight/report.yaml"],
            check=True, capture_output=True, text=True,
        ).stdout
        assert "render.json" in manifest, "the archive renders no preflight report"


if __name__ == "__main__":
    check_hook_log_policy()
    check_csi_hook_deadlines()
    check_report_schema()
    check_offline_never_blocks()
    check_catalog_matches_engine_and_schema()
    check_values_are_closed()
    check_chart_archive_carries_the_preflight()
    print("Preflight render checks passed.")
