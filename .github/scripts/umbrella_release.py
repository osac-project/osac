"""Publish package-derived platform notes without changing immutable tags."""

import argparse
import html
import io
import json
import os
import re
import subprocess
import tarfile
from pathlib import Path
from typing import Any

import yaml

STABLE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\+[0-9A-Za-z.-]+)?$")
PACKAGE_URL = "https://github.com/orgs/osac-project/packages/container/package/charts%2Fosac"
GUIDE = "docs/guides/installation/customer-install-guide.md"


def gh(*arguments: str) -> str:
    """Capture GitHub CLI output and propagate failures to the publishing job."""
    return subprocess.check_output(["gh", *arguments], text=True)


def stable_key(version: str) -> tuple[int, ...] | None:
    """Order stable SemVer cores numerically while excluding evaluation versions."""
    match = STABLE.fullmatch(version)
    return tuple(map(int, match.groups())) if match else None


def package_manifest(package: Path, version: str) -> dict[str, Any]:
    """Validate umbrella identity and resolve dependencies from its published package."""
    with tarfile.open(package, "r:gz") as archive:

        def read_yaml(name: str) -> dict[str, Any]:
            """Read chart metadata directly from the archive without extracting paths."""
            return yaml.safe_load(archive.extractfile(name))

        chart = read_yaml("osac/Chart.yaml")
        if chart["name"] != "osac" or str(chart["version"]) != version:
            raise ValueError("Published package does not match the requested umbrella version")
        lock = read_yaml("osac/Chart.lock")
        locked = {entry["name"]: entry for entry in lock["dependencies"]}
        bundled = {}
        for member in archive.getmembers():
            parts = member.name.split("/")
            if len(parts) == 4 and parts[:2] == ["osac", "charts"] and parts[-1] == "Chart.yaml":
                metadata = read_yaml(member.name)
                bundled[parts[2]] = str(metadata["version"])
            elif len(parts) == 3 and parts[:2] == ["osac", "charts"] and parts[-1].endswith(".tgz"):
                with tarfile.open(fileobj=io.BytesIO(archive.extractfile(member).read()), mode="r:gz") as dependency:
                    for nested in dependency.getmembers():
                        if len(nested.name.split("/")) == 2 and nested.name.endswith("/Chart.yaml"):
                            metadata = yaml.safe_load(dependency.extractfile(nested))
                            dep_name, dep_version = metadata["name"], str(metadata["version"])
                            if dep_name in bundled and bundled[dep_name] != dep_version:
                                raise ValueError(f"Ambiguous packaged versions for {dep_name}")
                            bundled[dep_name] = dep_version
        rows = []
        for dependency in chart.get("dependencies", []):
            name = dependency["name"]
            entry = locked.get(name, {})
            packaged_version = bundled.get(dependency.get("alias", name), bundled.get(name))
            resolved = str(entry["version"]) if "version" in entry else packaged_version
            if packaged_version and resolved != packaged_version:
                raise ValueError(f"Package/lock version mismatch for {name}")
            repository = entry.get("repository", dependency.get("repository", "unknown"))
            provenance = repository
            if not repository.startswith("oci://"):
                provenance += " (local fallback; not a published dependency)"
            if not entry:
                provenance += "; missing lock entry"
            rows.append({"chart": name, "version": resolved, "source": provenance})
    return {"chart": "osac", "version": version, "dependencies": rows}


def release_notes(manifest: dict[str, Any], component_versions: dict[str, str], repo: str) -> str:
    """Render package inventory, pinned installation steps, and upgrade precautions."""
    version = manifest["version"]
    rows = ["| Chart | Exact packaged/locked version | Source |", "| --- | --- | --- |"]
    for entry in manifest["dependencies"]:
        rows.append(f"| {entry['chart']} | {entry['version'] or 'unknown (not recorded)'} | {entry['source']} |")
    csi_version = component_versions.get("osac-csi-driver")
    standalone = csi_version or "not resolved/published this run"
    return f"""OSAC platform umbrella Helm chart **{version}**.

[Published chart package]({PACKAGE_URL})

OCI reference: `oci://ghcr.io/osac-project/charts/osac` (version `{version}`).

### Installed umbrella dependencies

These are the dependencies recorded in the published package and its Chart.lock,
including reused versions and CRD charts. Conditional charts require their values
toggles to be enabled. Local fallbacks are not claimed as published OCI charts.

{chr(10).join(rows)}

### Separately published standalone charts (not umbrella dependencies)

| Chart | Resolved version |
| --- | --- |
| csi-driver | {standalone} |
| csi-backends | {standalone} |

### Install this version

First complete the prerequisites and configure `my-values.yaml` using the
[customer installation guide](https://github.com/{repo}/blob/osac/v{version}/{GUIDE}).
Choose a namespace and replace `apps.example.com` with the cluster's application
domain before running:

```sh
export NS=osac
export DOMAIN=apps.example.com
helm install osac oci://ghcr.io/osac-project/charts/osac --version '{version}' \\
  --namespace "$NS" --create-namespace --values my-values.yaml \\
  --set global.clusterDomain="$DOMAIN" \\
  --set service.externalHostname="fulfillment-api-$NS.$DOMAIN" \\
  --set service.internalHostname="fulfillment-internal-api-$NS.$DOMAIN" \\
  --wait --timeout 40m
```

### Upgrade notes

This manifest is a version inventory, not a compatibility or migration guarantee.
Before upgrading, compare it with your installed release, review the changed
components' release notes and the installation guide, and back up configuration
and persistent data. Check CRD migration requirements and required values changes;
do not assume Helm upgrades CRDs automatically. No unverified upgrade path is
asserted here. Prereleases/nightlies are for evaluation, not the stable recommendation.
"""


def slack_chart_summary(package: Path, version: str, component_versions: dict[str, str]) -> str:
    """List the exact bundled versions, including dependencies reused from earlier releases."""
    manifest = package_manifest(package, version)

    def cell(value: str) -> str:
        return html.escape(value.replace("\r", " ").replace("\n", " ").replace("`", "'"), quote=False)

    rows = []
    for dependency in manifest["dependencies"]:
        resolved = dependency["version"] or "unknown (not recorded)"
        source = dependency["source"]
        if not source.startswith("oci://") or "missing lock entry" in source:
            resolved += f" [{source}]"
        rows.append((cell(dependency["chart"]), cell(resolved)))
    width = max([len("Chart"), *(len(name) for name, _ in rows)])
    table = "\n".join([f"{'Chart':<{width}}  Version", *(f"{name:<{width}}  {value}" for name, value in rows)])
    standalone = cell(component_versions.get("osac-csi-driver") or "not resolved this run")
    summary = (
        f"*OSAC umbrella chart:* `osac {cell(version)}`\n"
        f"<{PACKAGE_URL}|Published umbrella package>\n\n"
        f"*Bundled subcharts (includes reused versions):*\n```\n{table}\n```\n\n"
        "*Standalone charts (not installed by the umbrella):*\n"
        f"```\ncsi-driver    {standalone}\ncsi-backends  {standalone}\n```"
    )
    if len(summary) > 3000:
        raise ValueError("Chart inventory exceeds Slack's section limit; split the inventory into more sections")
    return summary


def publish(package: Path, version: str, component_versions: dict[str, str], repo: str) -> None:
    """Publish the inventory and notes while preserving any newer stable Latest."""
    manifest = package_manifest(package, version)
    notes = release_notes(manifest, component_versions, repo)
    Path("umbrella-dependencies.json").write_text(json.dumps(manifest, indent=2) + "\n")
    Path("umbrella-release-notes.md").write_text(notes)
    pages = json.loads(gh("api", "--paginate", "--slurp", f"repos/{repo}/releases?per_page=100"))
    releases = [release for page in pages for release in page]
    current = stable_key(version)
    candidates = [
        stable_key(release["tag_name"].removeprefix("osac/v"))
        for release in releases
        if release["tag_name"].startswith("osac/v") and not release["draft"] and not release["prerelease"]
    ]
    higher_stable = any(candidate is not None and candidate > current for candidate in candidates) if current else False
    tag = f"osac/v{version}"
    operation = "edit" if any(release["tag_name"] == tag for release in releases) else "create"
    gh(
        "release",
        operation,
        tag,
        "--repo",
        repo,
        "--title",
        f"OSAC Platform Chart v{version}",
        "--notes-file",
        "umbrella-release-notes.md",
        "--verify-tag",
        f"--latest={'true' if current and not higher_stable else 'false'}",
        f"--prerelease={'false' if current else 'true'}",
        "--draft=false",
    )
    gh("release", "upload", tag, "umbrella-dependencies.json", "--repo", repo, "--clobber")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--slack-summary", action="store_true", help="Print the chart inventory without publishing")
    arguments = parser.parse_args()
    package = Path(os.environ["PACKAGE"])
    version = os.environ["VERSION"]
    component_versions = json.loads(os.environ["COMPONENT_VERSIONS"])
    if arguments.slack_summary:
        print(slack_chart_summary(package, version, component_versions))
    else:
        publish(package, version, component_versions, os.environ["REPO"])
