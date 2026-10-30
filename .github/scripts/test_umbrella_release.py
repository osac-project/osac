"""Exercise the workflow's real release consumers with synthetic data and fake gh."""

import ast
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import yaml
from umbrella_release import GUIDE, PACKAGE_URL, package_manifest, slack_chart_summary

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/osac-build-and-publish.yaml").read_text())
FAKE_GH = r"""#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path

arguments = sys.argv[1:]
with Path("gh-calls.jsonl").open("a") as output:
    output.write(json.dumps(arguments) + "\n")
if os.environ.get("FAIL") and os.environ["FAIL"] in arguments:
    sys.exit(9)
if arguments == ["api", "--paginate", "--slurp", "repos/osac-project/osac/releases?per_page=100"]:
    print(os.environ.get("API_RESPONSE", Path("releases.json").read_text()))
elif arguments[:2] in (["release", "create"], ["release", "edit"]):
    if arguments[2] != "graph-latest":
        assert "--verify-tag" in arguments
    assert any(option.startswith("--latest=") for option in arguments)
    releases = json.loads(Path("releases.json").read_text())
    if arguments[1] == "create":
        assert not any(entry["tag_name"] == arguments[2] for page in releases for entry in page)
        releases[0].append({"tag_name": arguments[2], "draft": False, "prerelease": "--prerelease=true" in arguments})
        Path("releases.json").write_text(json.dumps(releases))
elif arguments[:2] == ["release", "upload"]:
    assert arguments[3:] == ["umbrella-dependencies.json", "--repo", "osac-project/osac", "--clobber"]
    assert Path(arguments[3]).is_file()
elif arguments[:3] == ["api", "-X", "DELETE"]:
    assert arguments[3] == "repos/osac-project/osac/git/refs/tags/v7.4.2"
else:
    raise AssertionError(f"Unexpected gh operation: {arguments}")
"""


def release(tag: str, prerelease: bool = False, draft: bool = False) -> dict[str, str | bool]:
    """Create the release API fields used to select the stable recommendation."""
    return {"tag_name": tag, "prerelease": prerelease, "draft": draft}


def condition(job: str, mode: str, results: dict[str, str], cancelled: bool = False) -> bool:
    """Interpret the workflow's limited condition syntax without executing Python."""
    expression = WORKFLOW["jobs"][job]["if"]
    expression = re.sub(r"needs\.([\w-]+)\.result", lambda match: repr(results[match[1]]), expression)
    expression = expression.replace("inputs.mode", repr(mode)).replace("&&", " and ").replace("||", " or ")
    expression = expression.replace("!cancelled()", "not cancelled()")
    statuses = {"always": True, "cancelled": cancelled, "failure": "failure" in results.values()}

    def interpret(node: ast.AST) -> str | bool | list[str]:
        """Allow literals, boolean operators, equality, and named workflow functions."""
        if isinstance(node, ast.Constant) and isinstance(node.value, (str, bool)):
            return node.value
        if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
            # Visit every branch so short-circuiting cannot hide unsupported syntax.
            values = [interpret(value) for value in node.values]
            return all(values) if isinstance(node.op, ast.And) else any(values)
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
            return not interpret(node.operand)
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.Eq):
            return interpret(node.left) == interpret(node.comparators[0])
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and not node.keywords:
            arguments = [interpret(argument) for argument in node.args]
            if node.func.id in statuses and not arguments:
                return statuses[node.func.id]
            if node.func.id == "fromJSON" and len(arguments) == 1 and isinstance(arguments[0], str):
                choices = json.loads(arguments[0])
                if isinstance(choices, list) and all(isinstance(choice, str) for choice in choices):
                    return choices
            if (
                node.func.id == "contains"
                and len(arguments) == 2
                and isinstance(arguments[0], list)
                and isinstance(arguments[1], str)
            ):
                return arguments[1] in arguments[0]
        raise ValueError(f"Unsupported workflow condition syntax: {ast.dump(node)}")

    try:
        tree = ast.parse(" ".join(expression.split()), mode="eval")
    except SyntaxError as error:
        raise ValueError("Invalid workflow condition syntax") from error
    return bool(interpret(tree.body))


class UmbrellaReleaseTests(unittest.TestCase):
    def setUp(self) -> None:
        """Isolate release files and replace external publishers with local fakes."""
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        (self.bin / "gh").write_text(FAKE_GH)
        (self.bin / "gh").chmod(0o755)
        (self.bin / "uv").write_text(
            "#!/bin/bash\nset -euo pipefail\n"
            'expected="run --no-project --with pyyaml==6.0.3 python .github/scripts/umbrella_release.py"\n'
            '[[ "$*" == "$expected" || "$*" == "$expected --slack-summary" ]]\n'
            'exec "$PYTHON" "${@:6}"\n'
        )
        (self.bin / "uv").chmod(0o755)
        script_dir = self.directory / ".github/scripts"
        script_dir.mkdir(parents=True)
        (script_dir / "umbrella_release.py").symlink_to(ROOT / ".github/scripts/umbrella_release.py")
        self.version = "7.4.2"
        self.package = self.directory / f"umbrella-artifacts/osac-installer/osac-{self.version}.tgz"
        self.package.parent.mkdir(parents=True)
        self.component_versions = {"osac-csi-driver": "3.2.1"}
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "HOME": str(self.directory),
            "VERSION": self.version,
            "PACKAGE": str(self.package),
            "REPO": "osac-project/osac",
            "COMPONENT_VERSIONS": json.dumps(self.component_versions),
            "GH_TOKEN": "synthetic-test-token",
            "PYTHON": sys.executable,
        }
        self.set_releases([])
        self.fixture()

    def set_releases(self, releases: list[dict[str, str | bool]]) -> None:
        """Store synthetic paginated API results for release selection checks."""
        (self.directory / "releases.json").write_text(json.dumps([releases]))

    def fixture(
        self,
        fallback: bool = False,
        missing_lock: bool = False,
        mismatch: bool = False,
        missing_metadata: bool = False,
        nested: bool = False,
    ) -> None:
        """Package chart and lock metadata with optional fallback or mismatch cases."""
        chart = yaml.safe_load((ROOT / "osac-installer/charts/osac/Chart.yaml").read_text())
        chart["version"] = self.env["VERSION"]
        entries = {}
        locks = []
        for index, dependency in enumerate(chart["dependencies"]):
            version = f"2.{index}.3"
            if "operator-crds" in dependency["name"]:
                version = "2.9.3"
            if dependency["name"] in ("osac-operator", "bare-metal-fulfillment-operator"):
                version = "2.9.3"
            repository = "file://fixture" if fallback and index == 0 else "oci://ghcr.io/osac-project/charts"
            dependency.update(repository=repository, version=">=0.0.0-0" if fallback else version)
            if not (missing_lock and index == 0):
                locks.append({"name": dependency["name"], "version": version, "repository": repository})
            entries[f"osac/charts/{dependency.get('alias', dependency['name'])}/Chart.yaml"] = {
                "name": dependency["name"],
                "version": "9.9.9" if mismatch and index == 0 else version,
            }
            if missing_metadata and index == 0:
                del entries[f"osac/charts/{dependency.get('alias', dependency['name'])}/Chart.yaml"]
        entries["osac/Chart.yaml"] = chart
        entries["osac/Chart.lock"] = {"dependencies": locks}
        with tarfile.open(self.package, "w:gz") as archive:
            for name, value in entries.items():
                content = yaml.safe_dump(value).encode()
                if nested and name.startswith("osac/charts/"):
                    nested_content = io.BytesIO()
                    with tarfile.open(fileobj=nested_content, mode="w:gz") as dependency_archive:
                        metadata = tarfile.TarInfo(f"{value['name']}/Chart.yaml")
                        metadata.size = len(content)
                        dependency_archive.addfile(metadata, io.BytesIO(content))
                    content = nested_content.getvalue()
                    name = f"osac/charts/{value['name']}-{value['version']}.tgz"
                member = tarfile.TarInfo(name)
                member.size = len(content)
                archive.addfile(member, io.BytesIO(content))

    def run_shell(self, script: str, **environment: str) -> subprocess.CompletedProcess[str]:
        """Run a consumer with strict Bash settings and isolated publisher fakes."""
        return subprocess.run(
            ["bash", "-euo", "pipefail", "-c", script],
            cwd=self.directory,
            env=self.env | environment,
            capture_output=True,
            text=True,
        )

    def run_release(self, **environment: str) -> subprocess.CompletedProcess[str]:
        """Exercise the workflow's actual publication step with synthetic inputs."""
        job = WORKFLOW["jobs"]["create-umbrella-release"]
        script = next(
            step["run"] for step in job["steps"] if step.get("name") == "Create or update umbrella chart release"
        )
        return self.run_shell(script, **environment)

    def calls(self) -> list[list[str]]:
        """Read captured GitHub CLI arguments to inspect publication side effects."""
        return [json.loads(line) for line in (self.directory / "gh-calls.jsonl").read_text().splitlines()]

    def run_notification(
        self, job_name: str = "notify-release", **environment: str
    ) -> subprocess.CompletedProcess[str]:
        """Execute the actual notification shell with curl captured locally and uv offline."""
        scripts = self.directory / "osac-installer/scripts"
        if not scripts.exists():
            scripts.parent.mkdir()
            scripts.symlink_to(ROOT / "osac-installer/scripts", target_is_directory=True)
        artifacts = self.directory / "nightly-artifacts"
        if not artifacts.exists():
            artifacts.mkdir()
            (artifacts / "images.txt").write_text("ghcr.io/osac-project/osac-operator:2.9.3\n")
            # Reproduce the real all-pinned release: no component source fragments.
            (artifacts / "chart-sources.txt").write_text(f"osac {self.version}\n")
            (artifacts / "osac-installer").mkdir()
            (artifacts / f"osac-installer/osac-{self.version}.tgz").symlink_to(self.package)
        (self.bin / "curl").write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, sys\n"
            "from pathlib import Path\n"
            "arguments = sys.argv[1:]\n"
            "assert arguments[arguments.index('-X') + 1] == 'POST'\n"
            "assert 'https://slack.invalid/local-capture' in arguments\n"
            "payload = arguments[arguments.index('-d') + 1]\n"
            "assert payload.startswith('@')\n"
            "with Path('slack-payloads.jsonl').open('a') as output:\n"
            "    output.write(json.dumps(json.loads(Path(payload[1:]).read_text())) + '\\n')\n"
            "sys.exit(int(os.environ.get('CURL_EXIT', '0')))\n"
        )
        (self.bin / "curl").chmod(0o755)
        (self.directory / "outputs.txt").write_text("")
        script = next(step["run"] for step in WORKFLOW["jobs"][job_name]["steps"] if "run" in step)
        script = script.replace("/tmp/slack-payload.json", str(self.directory / "slack-payload.json"))
        inputs = {
            "PACKAGE": str(artifacts / f"osac-installer/osac-{self.version}.tgz"),
            "MODE": "release",
            "SERVER_URL": "https://github.com",
            "REPOSITORY": "osac-project/osac",
            "RUN_ID": "12345",
            "SLACK_WEBHOOK_URL": "https://slack.invalid/local-capture",
            "GITHUB_OUTPUT": str(self.directory / "outputs.txt"),
            "SKIP_E2E": "false",
            "SKIP_INTEGRATION": "false",
            "SKIP_UNIT": "false",
            "SKIPPED_TESTS": "",
            "IMAGE_BUILDS": "success",
            "IMAGE_RELEASE": "success",
            "CHART_TAG_PUSHED": "true",
            "CHART_RELEASE": "success",
            "WORKFLOW_URL": "https://github.com/osac-project/osac/actions/runs/12345",
        }
        return self.run_shell(script, **(inputs | environment))

    def slack_payload(self) -> dict[str, Any]:
        """Read the most recently captured Slack request, checking Slack block limits."""
        payload = json.loads((self.directory / "slack-payloads.jsonl").read_text().splitlines()[-1])
        for block in payload["blocks"]:
            if block["type"] in ("header", "section"):
                self.assertLessEqual(len(block["text"]["text"]), 150 if block["type"] == "header" else 3000)
        return payload

    def test_slack_inventory_includes_reused_dependencies_and_separates_csi(self) -> None:
        """An all-pinned cut must show the package inventory even with no chart fragments."""
        result = self.run_notification()
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = self.slack_payload()
        self.assertIn("Release 7.4.2 - GHCR published", payload["blocks"][0]["text"]["text"])
        self.assertIn("Konflux staging:* pending", payload["blocks"][1]["text"]["text"])
        self.assertIn("\n", payload["blocks"][1]["text"]["text"])
        summary = payload["blocks"][2]["text"]["text"]
        self.assertTrue(summary.startswith("*OSAC umbrella chart:* `osac 7.4.2`"))
        self.assertIn(PACKAGE_URL, summary)
        bundled, standalone = summary.split("*Standalone charts")
        for entry in package_manifest(self.package, self.version)["dependencies"]:
            self.assertRegex(bundled, rf"{re.escape(entry['chart'])}\s+{re.escape(entry['version'])}")
        self.assertNotIn("csi-driver", bundled)
        self.assertIn("csi-driver    3.2.1", standalone)
        self.assertIn("csi-backends  3.2.1", standalone)
        self.assertFalse((self.directory / "gh-calls.jsonl").exists())
        self.assertEqual((self.directory / "outputs.txt").read_text(), "skipped-tests=\n")

    def test_slack_inventory_fallback_and_unresolved_versions(self) -> None:
        """Local and missing dependency metadata must be visible instead of claimed as published."""
        for nested, missing_metadata in ((False, False), (True, False), (False, True)):
            with self.subTest(nested=nested, missing_metadata=missing_metadata):
                self.fixture(fallback=True, missing_lock=True, nested=nested, missing_metadata=missing_metadata)
                result = self.run_notification(COMPONENT_VERSIONS="{}")
                self.assertEqual(result.returncode, 0, result.stderr)
                summary = self.slack_payload()["blocks"][2]["text"]["text"]
                self.assertIn("local fallback; not a published dependency", summary)
                self.assertIn("missing lock entry", summary)
                self.assertIn("unknown (not recorded)" if missing_metadata else "2.9.3", summary)
                self.assertIn("not resolved this run", summary)

    def test_slack_invalid_package_prevents_send(self) -> None:
        """Missing packages and version mismatches must fail before sending Slack."""
        for inputs in ({"PACKAGE": str(self.directory / "missing.tgz")}, {"VERSION": "0.0.0"}):
            with self.subTest(inputs=inputs):
                self.assertNotEqual(self.run_notification(**inputs).returncode, 0)
                self.assertFalse((self.directory / "slack-payloads.jsonl").exists())
        self.fixture(mismatch=True)
        self.assertNotEqual(self.run_notification().returncode, 0)
        self.assertFalse((self.directory / "slack-payloads.jsonl").exists())
        self.assertFalse((self.directory / "gh-calls.jsonl").exists())

    def test_slack_nightly_and_skipped_validation_gates(self) -> None:
        """Nightly messaging must exclude Konflux and carry skipped gates to the final message."""
        self.env["VERSION"] = "7.4.2-nightly.20261029.abc.123.1"
        self.fixture()
        result = self.run_notification(MODE="nightly", SKIP_E2E="true", SKIP_INTEGRATION="true", SKIP_UNIT="true")
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = self.slack_payload()
        self.assertIn("Nightly Build", payload["blocks"][0]["text"]["text"])
        self.assertIn("e2e,integration,unit SKIPPED", payload["blocks"][0]["text"]["text"])
        self.assertIn("not run for nightly builds", payload["blocks"][1]["text"]["text"])
        self.assertIn(self.env["VERSION"], payload["blocks"][2]["text"]["text"])
        self.assertEqual((self.directory / "outputs.txt").read_text(), "skipped-tests=e2e,integration,unit\n")

    def test_konflux_complete_only_after_both_staging_releases_succeed(self) -> None:
        """Complete must retain the warning when the release skipped validation gates."""
        for skipped in ("", "e2e,integration,unit"):
            with self.subTest(skipped=skipped):
                result = self.run_notification("notify-konflux", SKIPPED_TESTS=skipped)
                self.assertEqual(result.returncode, 0, result.stderr)
                payload = self.slack_payload()
                header = payload["blocks"][0]["text"]["text"]
                self.assertIn("Release 7.4.2 - Complete", header)
                self.assertTrue(header.startswith(":warning:" if skipped else ":white_check_mark:"))
                if skipped:
                    self.assertIn(f"{skipped} SKIPPED", header)
                text = payload["blocks"][1]["text"]["text"]
                self.assertIn("Konflux image staging:* success", text)
                self.assertIn("Konflux chart staging:* success", text)

    def test_konflux_build_and_release_failures_never_announce_complete(self) -> None:
        """Required image failures and tolerated chart failures must report failed staging."""
        cases = (
            {"IMAGE_BUILDS": "failure", "IMAGE_RELEASE": "skipped", "CHART_RELEASE": "not completed"},
            {"IMAGE_RELEASE": "failure", "CHART_RELEASE": "skipped"},
            {"CHART_RELEASE": "failure"},
        )
        for inputs in cases:
            with self.subTest(inputs=inputs):
                result = self.run_notification("notify-konflux", **inputs)
                self.assertEqual(result.returncode, 0, result.stderr)
                header = self.slack_payload()["blocks"][0]["text"]["text"]
                self.assertTrue(header.startswith(":x:"))
                self.assertIn("Konflux staging failed", header)
                self.assertNotIn("Complete", header)

    def test_konflux_skips_and_missing_outputs_report_incomplete(self) -> None:
        """A failed stamping step, skipped release, or timeout must never look complete."""
        for outcome in ("skipped", "not completed", "cancelled"):
            with self.subTest(outcome=outcome):
                result = self.run_notification("notify-konflux", CHART_TAG_PUSHED="", CHART_RELEASE=outcome)
                self.assertEqual(result.returncode, 0, result.stderr)
                payload = self.slack_payload()
                self.assertIn("Konflux staging incomplete", payload["blocks"][0]["text"]["text"])
                self.assertIn("chart build tag pushed:* false", payload["blocks"][1]["text"]["text"])
                self.assertIn(f"chart staging:* {outcome}", payload["blocks"][1]["text"]["text"])

    def test_slack_send_errors_fail_the_notification(self) -> None:
        """Slack HTTP/transport failure must propagate from both actual notification consumers."""
        for job in ("notify-release", "notify-konflux"):
            with self.subTest(job=job):
                self.assertEqual(self.run_notification(job, CURL_EXIT="22").returncode, 22)
                self.assertIn("blocks", self.slack_payload())

    def test_slack_inventory_escapes_metadata_and_rejects_oversized_sections(self) -> None:
        """Untrusted chart metadata cannot inject Slack mentions or overflow section limits."""
        manifest = {"dependencies": [{"chart": "unsafe<@here>`\nname", "version": "1.2.3", "source": "oci://example"}]}
        with patch("umbrella_release.package_manifest", return_value=manifest):
            summary = slack_chart_summary(self.package, self.version, {})
            self.assertIn("unsafe&lt;@here&gt;' name", summary)
            self.assertNotIn("<@here>", summary)
        manifest["dependencies"][0]["chart"] = "x" * 3001
        with (
            patch("umbrella_release.package_manifest", return_value=manifest),
            self.assertRaisesRegex(ValueError, "section limit"),
        ):
            slack_chart_summary(self.package, self.version, {})

    def test_create_edit_and_deterministic_inventory(self) -> None:
        """Retries must retain package-derived inventory and customer install guidance."""
        self.assertEqual(self.run_release().returncode, 0)
        self.assertIn("--latest=true", self.calls()[1])
        self.assertIn("--prerelease=false", self.calls()[1])
        notes = (self.directory / "umbrella-release-notes.md").read_text()
        inventory = (self.directory / "umbrella-dependencies.json").read_text()
        self.assertEqual(len(json.loads(inventory)["dependencies"]), 8)
        self.assertIn("| osac-operator-crds | 2.9.3 |", notes)
        self.assertIn("| bare-metal-fulfillment-operator-crds | 2.9.3 |", notes)
        self.assertIn("| csi-driver | 3.2.1 |", notes)
        installed, standalone = notes.split("### Separately published standalone charts")
        self.assertNotIn("csi-driver", installed)
        self.assertIn("not umbrella dependencies", standalone)
        self.assertIn(PACKAGE_URL, notes)
        self.assertIn("oci://ghcr.io/osac-project/charts/osac", notes)
        self.assertIn("--version '7.4.2'", notes)
        self.assertIn("--values my-values.yaml", notes)
        self.assertIn("--namespace", notes)
        self.assertIn(GUIDE, notes)
        self.assertTrue((ROOT / GUIDE).is_file())
        self.assertIn("not a compatibility or migration guarantee", notes)
        self.assertEqual(self.run_release().returncode, 0)
        self.assertEqual(self.calls()[4][1], "edit")
        self.assertEqual(notes, (self.directory / "umbrella-release-notes.md").read_text())
        self.assertEqual(inventory, (self.directory / "umbrella-dependencies.json").read_text())

    def test_higher_stable_and_paginated_releases(self) -> None:
        """A newer stable umbrella on a later API page must keep Latest."""
        (self.directory / "releases.json").write_text(
            json.dumps(
                [
                    [release("osac-operator/v99.0.0"), release("osac/v99.0.0", draft=True)],
                    [release("osac/v99.0.0-rc.1", prerelease=True), release("osac/v8.0.0")],
                ]
            )
        )
        self.assertEqual(self.run_release().returncode, 0)
        self.assertIn("--latest=false", self.calls()[1])
        self.assertEqual(self.run_release().returncode, 0)
        self.assertIn("--latest=false", self.calls()[4])

    def test_numeric_semver_build_metadata_and_drafts(self) -> None:
        """Only published stable umbrella versions affect numeric version ordering."""
        cases = [
            ("7.4.2+build-rc.1", "osac/v7.4.1", False, False, True),
            ("7.4.2", "osac/v7.4.2+other.2", False, False, True),
            ("7.9.0", "osac/v7.10.0+build.1", False, False, False),
            ("7.10.0", "osac/v7.9.0", False, False, True),
            ("7.4.2", "osac/v99.0.0", False, True, True),
            ("7.4.2", "osac/v99.0.0-rc.1", True, False, True),
            ("7.4.2", "osac-operator/v99.0.0", False, False, True),
            ("7.4.2", "osac/v7.4.2", False, True, True),
        ]
        for version, tag, prerelease, draft, latest in cases:
            with self.subTest(version=version, tag=tag, draft=draft):
                self.env["VERSION"] = version
                self.fixture()
                self.set_releases([release(tag, prerelease=prerelease, draft=draft)])
                self.assertEqual(self.run_release().returncode, 0)
                arguments = self.calls()[-2]
                self.assertIn(f"--latest={str(latest).lower()}", arguments)
                self.assertIn("--prerelease=false", arguments)
                self.assertIn("--draft=false", arguments)
                self.assertEqual(arguments[1], "edit" if tag == f"osac/v{version}" else "create")

    def test_invalid_platform_api_data_fails_before_mutation(self) -> None:
        """Malformed release API responses must prevent all publication writes."""
        for response in ("{invalid", '{"message": "unexpected"}', "[null]"):
            with self.subTest(response=response):
                (self.directory / "gh-calls.jsonl").write_text("")
                self.assertNotEqual(self.run_release(API_RESPONSE=response).returncode, 0)
                self.assertEqual(len(self.calls()), 1)

    def test_documented_install_command_is_usable_shell(self) -> None:
        """The copied install example must pass the intended version and hostnames."""
        self.assertEqual(self.run_release().returncode, 0)
        notes = (self.directory / "umbrella-release-notes.md").read_text()
        script = notes.split("```sh\n", 1)[1].split("```", 1)[0]
        (self.bin / "helm").write_text('#!/bin/bash\nset -euo pipefail\nprintf "%s\\n" "$@" > helm-args.txt\n')
        (self.bin / "helm").chmod(0o755)
        self.assertEqual(self.run_shell(script).returncode, 0)
        arguments = (self.directory / "helm-args.txt").read_text().splitlines()
        self.assertEqual(arguments[:3], ["install", "osac", "oci://ghcr.io/osac-project/charts/osac"])
        self.assertEqual(arguments[arguments.index("--version") + 1], self.version)
        self.assertIn("service.externalHostname=fulfillment-api-osac.apps.example.com", arguments)
        self.assertIn("service.internalHostname=fulfillment-internal-api-osac.apps.example.com", arguments)

    def test_prerelease_and_nightly_suffixes_never_latest(self) -> None:
        """Evaluation versions must stay prereleases across creation and retries."""
        for version in ("7.4.2-rc.1", "7.4.2-nightly.20261027.abc.1.1"):
            with self.subTest(version=version):
                self.env["VERSION"] = version
                self.fixture()
                self.assertEqual(self.run_release().returncode, 0)
                self.assertEqual(self.run_release().returncode, 0)
                for arguments in self.calls():
                    if arguments[:2] in (["release", "create"], ["release", "edit"]):
                        self.assertIn("--latest=false", arguments)
                        self.assertIn("--prerelease=true", arguments)

    def test_failures_are_not_treated_as_existing_releases(self) -> None:
        """API, tag verification, and publication failures must propagate to the job."""
        for operation in ("api", "create", "upload", "--verify-tag"):
            with self.subTest(operation=operation):
                self.set_releases([])
                (self.directory / "gh-calls.jsonl").write_text("")
                self.assertNotEqual(self.run_release(FAIL=operation).returncode, 0)
                self.assertFalse(any(call[:2] == ["release", "edit"] for call in self.calls()))
        self.set_releases([release("osac/v7.4.2")])
        self.assertNotEqual(self.run_release(FAIL="edit").returncode, 0)

    def test_local_and_missing_fallback_inventory(self) -> None:
        """Inventory must distinguish local fallbacks and unavailable package metadata."""
        self.fixture(fallback=True, missing_lock=True)
        self.env["COMPONENT_VERSIONS"] = "{}"
        self.assertEqual(self.run_release().returncode, 0)
        notes = (self.directory / "umbrella-release-notes.md").read_text()
        self.assertIn("local fallback; not a published dependency", notes)
        self.assertIn("missing lock entry", notes)
        self.assertIn("not resolved/published this run", notes)
        self.assertIn("| osac-operator-crds | 2.9.3 |", notes)
        self.fixture(fallback=True, missing_lock=True, missing_metadata=True)
        self.assertEqual(self.run_release().returncode, 0)
        self.assertIn("unknown (not recorded)", (self.directory / "umbrella-release-notes.md").read_text())

    def test_package_mismatch_blocks_release(self) -> None:
        """Conflicting package, lock, or requested versions must prevent publication."""
        self.fixture(mismatch=True)
        self.assertNotEqual(self.run_release().returncode, 0)
        self.assertFalse((self.directory / "gh-calls.jsonl").exists())
        with self.assertRaises(ValueError):
            package_manifest(self.package, "0.0.0")

    def test_nested_archives_supply_missing_lock_versions_and_reject_mismatch(self) -> None:
        """Nested subcharts must supply exact versions without changing umbrella identity."""
        self.fixture(fallback=True, missing_lock=True, nested=True)
        self.assertEqual(self.run_release().returncode, 0)
        manifest = json.loads((self.directory / "umbrella-dependencies.json").read_text())
        self.assertEqual(manifest["version"], self.version)
        self.assertEqual(manifest["dependencies"][0]["version"], "2.9.3")
        notes = (self.directory / "umbrella-release-notes.md").read_text()
        self.assertIn(f"OSAC platform umbrella Helm chart **{self.version}**.", notes)
        self.assertIn(f"--version '{self.version}'", notes)
        self.assertIn(f"/blob/osac/v{self.version}/{GUIDE}", notes)
        self.fixture(nested=True, mismatch=True)
        self.assertNotEqual(self.run_release().returncode, 0)

    def test_component_invalid_api_response_fails_closed(self) -> None:
        """The component publisher must stop when release discovery returns invalid JSON."""
        action = yaml.safe_load((ROOT / ".github/actions/create-component-release/action.yaml").read_text())
        result = self.run_shell(
            action["runs"]["steps"][0]["run"],
            API_RESPONSE="{invalid",
            TAG="osac-operator/v7.4.2",
            COMPONENT="osac-operator",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)

    def test_component_actual_shell_create_and_edit_opt_out(self) -> None:
        """Component releases must opt out of Latest on both creation and update."""
        action = yaml.safe_load((ROOT / ".github/actions/create-component-release/action.yaml").read_text())
        script = action["runs"]["steps"][0]["run"]
        for version in ("v7.4.2", "v7.4.2-rc.1"):
            for operation in ("create", "edit"):
                with self.subTest(version=version, operation=operation):
                    tag = f"osac-operator/{version}"
                    self.set_releases([release(tag)] if operation == "edit" else [])
                    result = self.run_shell(script, VERSION=version, TAG=tag, COMPONENT="osac-operator")
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("--latest=false", self.calls()[-1])
                    self.assertIn(f"--prerelease={'true' if '-' in version else 'false'}", self.calls()[-1])
        self.assertNotEqual(
            self.run_shell(script, FAIL="api", TAG="osac-operator/v7.4.2", COMPONENT="osac-operator").returncode, 0
        )

    def test_binary_consumer_and_goreleaser_opt_out(self) -> None:
        """Binary publication must retain scoped tags without taking umbrella Latest."""
        workflow = yaml.safe_load((ROOT / ".github/workflows/publish-binaries.yaml").read_text())
        script = next(
            step["run"]
            for step in workflow["jobs"]["publish-binaries"]["steps"]
            if step.get("name") == "Point release at the scoped trigger tag"
        )
        script = script.replace("${{ inputs.version }}", "v7.4.2").replace(
            "${{ inputs.tag }}", "fulfillment-service/v7.4.2"
        )
        script = script.replace("${{ github.repository }}", "osac-project/osac")
        self.assertEqual(self.run_shell(script).returncode, 0)
        self.assertIn("--latest=false", self.calls()[0])
        (self.directory / "gh-calls.jsonl").write_text("")
        self.assertNotEqual(self.run_shell(script, FAIL="edit").returncode, 0)
        self.assertEqual(len(self.calls()), 1)
        config = yaml.safe_load((ROOT / "fulfillment-service/.goreleaser.yaml").read_text())
        self.assertIs(config["release"]["make_latest"], False)
        self.assertEqual(config["release"]["prerelease"], "auto")

    def test_graphify_only_release_creation_opts_out(self) -> None:
        """The floating graph release must opt out of Latest and propagate failures."""
        workflow = yaml.safe_load((ROOT / ".github/workflows/graphify-brain-refresh.yaml").read_text())
        steps = [step for job in workflow["jobs"].values() for step in job["steps"]]
        script = next(step["run"] for step in steps if step.get("name") == "Publish to GH Release")
        creation = script[script.index("gh release create") : script.index("\nfi", script.index("gh release create"))]
        result = self.run_shell(
            "assets=(graphify-bundle.tar.gz)\n" + creation,
            RELEASE_TAG="graph-latest",
            GITHUB_REPOSITORY="osac-project/osac",
            GITHUB_SHA="a" * 40,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--latest=false", self.calls()[0])
        self.assertEqual(script.count("--latest=false"), 1)
        self.assertNotEqual(
            self.run_shell(
                "assets=()\n" + creation,
                FAIL="create",
                RELEASE_TAG="graph-latest",
                GITHUB_REPOSITORY="osac-project/osac",
                GITHUB_SHA="a" * 40,
            ).returncode,
            0,
        )


class ReleaseWorkflowContractTests(unittest.TestCase):
    def test_final_notification_waits_for_every_konflux_stage_and_cleanup_waits_for_it(self) -> None:
        """Final status must not race any stage, while GHCR publication stays independent."""
        jobs = WORKFLOW["jobs"]
        stages = {
            "wait-for-konflux-build",
            "trigger-konflux-staging-release",
            "stamp-and-release-charts",
            "wait-for-konflux-chart-build",
            "trigger-konflux-chart-staging-release",
        }
        self.assertTrue(stages.issubset(jobs["notify-konflux"]["needs"]))
        self.assertIn("notify-release", jobs["notify-konflux"]["needs"])
        self.assertIn("notify-konflux", jobs["cleanup"]["needs"])
        self.assertTrue(stages.isdisjoint(jobs["notify-release"]["needs"]))
        self.assertTrue(stages.isdisjoint(jobs["create-umbrella-release"]["needs"]))

    def test_final_notification_covers_failed_and_skipped_stages_but_not_cancellation(self) -> None:
        """Failure/skip must still report a final outcome; nightly/cancelled runs must not."""
        results = {name: "success" for name in WORKFLOW["jobs"]}
        stages = [name for name in WORKFLOW["jobs"]["notify-konflux"]["needs"] if "konflux" in name]
        stages.append("stamp-and-release-charts")
        for mode in ("release", "nightly"):
            for cancelled in (False, True):
                for stage in stages:
                    for outcome in ("success", "failure", "skipped", "cancelled"):
                        with self.subTest(mode=mode, cancelled=cancelled, stage=stage, outcome=outcome):
                            self.assertEqual(
                                condition("notify-konflux", mode, results | {stage: outcome}, cancelled),
                                mode == "release" and not cancelled,
                            )
        for dependency in ("tag", "notify-release"):
            for outcome in ("failure", "skipped", "cancelled"):
                self.assertFalse(condition("notify-konflux", "release", results | {dependency: outcome}))

    def test_tolerated_chart_failures_use_step_outcome_instead_of_job_result(self) -> None:
        """A continue-on-error job result is not proof that chart staging succeeded."""
        jobs = WORKFLOW["jobs"]
        charts = jobs["trigger-konflux-chart-staging-release"]
        self.assertIs(charts["continue-on-error"], True)
        self.assertEqual(charts["outputs"]["release-outcome"], "${{ steps.release.outcome }}")
        release_step = next(step for step in charts["steps"] if step.get("id") == "release")
        self.assertIn("if not released:", release_step["run"])
        self.assertIn("sys.exit(1)", release_step["run"])
        notification = next(step for step in jobs["notify-konflux"]["steps"] if "run" in step)
        self.assertEqual(
            notification["env"]["CHART_RELEASE"],
            "${{ needs.trigger-konflux-chart-staging-release.outputs.release-outcome || 'not completed' }}",
        )
        self.assertEqual(notification["env"]["SKIPPED_TESTS"], "${{ needs.notify-release.outputs.skipped-tests }}")

    def test_ghcr_notification_consumes_exact_package_and_no_package_lookup_token(self) -> None:
        """The Slack inventory must use the published archive even without component fragments."""
        job = WORKFLOW["jobs"]["notify-release"]
        self.assertEqual(
            job["env"]["PACKAGE"], "nightly-artifacts/osac-installer/osac-${{ needs.prepare.outputs.version }}.tgz"
        )
        script = next(step["run"] for step in job["steps"] if "run" in step)
        self.assertIn("python .github/scripts/umbrella_release.py --slack-summary", script)
        self.assertNotIn("build_slack_charts_published_summary", script)
        self.assertNotIn("packages", job["permissions"])

    def test_conditions_reject_python_execution_and_unsupported_syntax(self) -> None:
        """Reject executable Python, including code in an unused boolean branch."""
        expressions = (
            "().__class__.__bases__[0].__subclasses__()",
            "always().__class__",
            "(lambda: True)()",
            "[value for value in 'abc']",
            "fromJSON('{}', object_hook=always)",
            "contains(*fromJSON('[\"success\"]'))",
            "always('unexpected')",
            "fromJSON('{}')",
            "1 + 1 == 2",
            "True if always() else False",
            "unknown()",
            "always(",
            "always() || ().__class__.__bases__[0].__subclasses__()",
            "cancelled() && ().__class__.__bases__[0].__subclasses__()",
        )
        for expression in expressions:
            with (
                self.subTest(expression=expression),
                patch.dict(WORKFLOW["jobs"], {"condition-fixture": {"if": expression}}),
                self.assertRaises(ValueError),
            ):
                condition("condition-fixture", "release", {})

    def test_notification_and_publication_matrix(self) -> None:
        """Notifications must reflect publication outcomes and respect cancellation."""
        jobs = WORKFLOW["jobs"]
        results = {name: "success" for name in jobs}
        for mode in ("release", "nightly"):
            for outcome in ("success", "failure", "skipped", "cancelled"):
                for cancelled in (False, True):
                    with self.subTest(mode=mode, outcome=outcome, cancelled=cancelled):
                        values = results | {"create-umbrella-release": outcome}
                        expected = not cancelled and (
                            outcome == "success" or (mode == "nightly" and outcome == "skipped")
                        )
                        self.assertEqual(condition("notify-release", mode, values, cancelled), expected)
                        self.assertEqual(
                            condition("notify-failure", mode, values, cancelled), outcome == "failure" and not cancelled
                        )
                        self.assertEqual(
                            condition("create-umbrella-release", mode, values, cancelled),
                            mode == "release" and not cancelled,
                        )
        for dependency in (
            "tag",
            "create-component-releases",
            "publish-fulfillment-service-binaries",
            "publish-fulfillment-service-proto",
        ):
            for outcome in ("failure", "cancelled"):
                with self.subTest(dependency=dependency, outcome=outcome):
                    values = results | {dependency: outcome}
                    self.assertFalse(condition("create-umbrella-release", "release", values))
                    self.assertFalse(condition("notify-release", "release", values))
            if dependency != "tag":
                self.assertTrue(condition("create-umbrella-release", "release", results | {dependency: "skipped"}))
        for name in ("notify-release", "notify-failure"):
            self.assertIn("create-umbrella-release", jobs[name]["needs"])
        self.assertIn("publish-umbrella", jobs["tag"]["needs"])
        self.assertEqual(jobs["create-umbrella-release"]["concurrency"]["cancel-in-progress"], False)

    def test_ci_consumes_tests_and_workflow_consumes_helper(self) -> None:
        """CI and publication must keep using the tested consumers and immutable guard."""
        ci = (ROOT / ".github/workflows/pre-commit.yaml").read_text()
        self.assertIn("unittest discover -s .github/scripts -p test_umbrella_release.py", ci)
        script = str(WORKFLOW["jobs"]["create-umbrella-release"]["steps"])
        self.assertIn(".github/scripts/umbrella_release.py", script)
        self.assertIn("nightly-${{ env.VERSION }}", script)
        self.assertIn("umbrella-artifacts/osac-installer/osac-${{ env.VERSION }}.tgz", script)
        workflow = (ROOT / ".github/workflows/osac-build-and-publish.yaml").read_text()
        self.assertIn("refusing to re-release an already-published version", workflow)
        self.assertNotIn("trigger-konflux", str(WORKFLOW["jobs"]["create-umbrella-release"]["needs"]))


if __name__ == "__main__":
    unittest.main()
