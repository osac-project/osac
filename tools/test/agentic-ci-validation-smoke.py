#!/usr/bin/env python3
"""Exercise the real sandbox selectors with Git/Bash and recording tool doubles."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[2]
MODULES = (
    "fulfillment-service",
    "osac-operator",
    "osac-operator/api",
    "bare-metal-fulfillment-operator",
    "osac-csi-driver",
    "osac-metering/schema",
    "osac-metering/adapters",
    "osac-metering/metering-service",
    "osac-ui/proxy",
)
METERING = {f"osac-metering/{name}" for name in ("schema", "adapters", "metering-service")}
DOUBLE = r"""
import json
import os
import sys
from pathlib import Path

tool = Path(sys.argv[0]).name
try:
    cwd = str(Path.cwd().relative_to(os.environ["FIXTURE_REPO"]))
except ValueError:
    cwd = str(Path.cwd())
record = {"tool": tool, "cwd": cwd, "args": sys.argv[1:]}
with open(os.environ["RECORD_LOG"], "a") as log:
    log.write(json.dumps(record) + "\n")
failure = json.loads(os.environ.get("FAIL_COMMAND", "{}"))
if failure and all(record.get(key) == value for key, value in failure.items()):
    sys.exit(17)
if tool == "go" and sys.argv[1] == "list":
    for name in json.loads(os.environ["GO_LIST_PACKAGES"]):
        print(f"example.test/{cwd}/internal/{name} {Path.cwd() / 'internal' / name}")
"""


class SandboxValidationTests(unittest.TestCase):
    """No OSAC service, toolchain download, cluster, or container is started."""

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory(prefix="osac-sandbox-smoke-")
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name) / "repo"
        self.repo.mkdir()
        self.bin = Path(self.tmp.name) / "bin"
        self.bin.mkdir()
        self.log = Path(self.tmp.name) / "commands.jsonl"
        self.venv = Path(self.tmp.name) / "e2e-venv"
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "FIXTURE_REPO": str(self.repo),
            "RECORD_LOG": str(self.log),
            "OSAC_E2E_VENV": str(self.venv),
            "GO_LIST_PACKAGES": json.dumps(["unit", "envtest", "container"]),
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
        }
        self.env.pop("FAIL_COMMAND", None)
        for tool in ("go", "make", "golangci-lint", "pnpm", "uv", "helm", "ruff"):
            script = self.bin / tool
            script.write_text(f"#!{sys.executable}\n{DOUBLE}")
            script.chmod(0o755)
        interpreter = self.venv / "bin/python"
        interpreter.parent.mkdir(parents=True)
        interpreter.write_text(f"#!{sys.executable}\n{DOUBLE}")
        interpreter.chmod(0o755)
        for module in MODULES:
            self.write(f"{module}/go.mod", "module example.test/fixture\n")
            self.write(f"{module}/go.sum", "")
            self.write(f"{module}/.golangci.yml", "version: '2'\n")
            for package, body in (("unit", "package unit"), ("envtest", "envtest."), ("container", "NewContainer(")):
                self.write(f"{module}/internal/{package}/fixture_test.go", body + "\n")
        for path in (
            "osac-ui/package.json",
            "osac-ui/pnpm-lock.yaml",
            "tests/e2e/test_example.py",
            "tests/__init__.py",
            "pyproject.toml",
            "go.work",
            "README.md",
        ):
            self.write(path, "baseline\n")
        helper = ROOT / ".agentic-ci/changed.sh"
        if helper.exists():
            self.write(".agentic-ci/changed.sh", helper.read_text())
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.commit()
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
        profile = yaml.safe_load((ROOT / ".agentic-ci/config.yml").read_text())["sandbox"]
        self.steps = {step["name"]: step["run"] for step in profile["validate"]}

    def write(self, path: str, body: str = "changed\n") -> None:
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body)

    def git(self, *args: str) -> str:
        return subprocess.check_output(["git", *args], cwd=self.repo, env=self.env, text=True).strip()

    def commit(self) -> None:
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")

    def run_step(self, name: str, failure: dict[str, Any] | None = None) -> subprocess.CompletedProcess[str]:
        self.log.write_text("")
        env = {**self.env, "FAIL_COMMAND": json.dumps(failure or {})}
        return subprocess.run(
            ["bash", "-c", self.steps[name]],
            cwd=self.repo,
            env=env,
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )

    def records(self, tool: str | None = None) -> list[dict[str, Any]]:
        records = [json.loads(line) for line in self.log.read_text().splitlines()]
        return [record for record in records if tool is None or record["tool"] == tool]

    def selected(self, step: str, tool: str) -> set[str]:
        result = self.run_step(step)
        self.assertEqual(result.returncode, 0, result.stderr)
        return {str(record["cwd"]) for record in self.records(tool)}

    def assert_changed(self, expected: bool, *paths: str) -> None:
        script = """set -euo pipefail
base=$(git merge-base origin/HEAD HEAD 2>/dev/null || git rev-parse -q --verify origin/HEAD || true)
source .agentic-ci/changed.sh
if changed "$@"; then echo yes; else echo no; fi
"""
        result = subprocess.run(
            ["bash", "-c", script, "fixture", *paths],
            cwd=self.repo,
            env=self.env,
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "yes" if expected else "no")

    def test_api_inputs_select_direct_checks_and_consumers(self) -> None:
        for path in ("internal/unit/fixture_test.go", "go.mod", "go.sum", "testdata/nested/new fixture.json"):
            with self.subTest(path=path):
                self.write(f"osac-operator/api/{path}")
                lint = self.run_step("go-lint")
                self.assertEqual(lint.returncode, 0, lint.stderr)
                self.assertIn(
                    {
                        "tool": "golangci-lint",
                        "cwd": "osac-operator/api",
                        "args": ["run", "--config", "../.golangci.yml", "./..."],
                    },
                    self.records(),
                )
                tests = self.selected("go-test", "go")
                self.assertTrue(
                    {"osac-operator/api", "fulfillment-service", "osac-operator", "bare-metal-fulfillment-operator"}
                    <= tests,
                    tests,
                )
                self.git("reset", "--hard", "HEAD")
                self.git("clean", "-fd")

    def test_parent_lint_config_selects_api_lint(self) -> None:
        self.write("osac-operator/.golangci.yml")
        self.assertIn("osac-operator/api", self.selected("go-lint", "golangci-lint"))

    def test_schema_inputs_select_all_metering_modules(self) -> None:
        for path in ("internal/unit/fixture_test.go", "go.mod", "go.sum", "testdata/nested/new.json"):
            with self.subTest(path=path):
                self.write(f"osac-metering/schema/{path}")
                result = self.run_step("go-lint")
                self.assertEqual(result.returncode, 0, result.stderr)
                modules = {record["args"][1] for record in self.records("make")}
                self.assertTrue(modules >= METERING, modules)
                self.assertTrue(self.selected("go-test", "go") >= METERING)
                self.git("reset", "--hard", "HEAD")
                self.git("clean", "-fd")

    def test_no_change_and_irrelevant_change_do_not_select_go_checks(self) -> None:
        for changed in (False, True):
            with self.subTest(changed=changed):
                if changed:
                    self.write("README.md")
                for step in ("go-lint", "go-test"):
                    result = self.run_step(step)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(self.records(), [])

    def test_broad_trigger_includes_api(self) -> None:
        self.write("go.work")
        self.assertIn("osac-operator/api", self.selected("go-test", "go"))

    def test_large_tracked_untracked_and_mixed_streams(self) -> None:
        names = [f"osac-operator/api/testdata/{number:04d}-{'x' * 110}" for number in range(4000)]
        for path in names:
            self.write(path, "baseline\n")
        self.commit()
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        for path in names:
            self.write(path)
        self.assert_changed(True, "osac-operator/api")
        for path in names:
            self.write(f"{path}-untracked")
        self.assert_changed(True, "osac-operator/api")
        self.git("reset", "--hard", "HEAD")
        self.assert_changed(True, "osac-operator/api")
        self.assert_changed(False, "osac-metering/schema")

    def test_spaces_deletions_committed_and_working_tree_changes(self) -> None:
        self.write("osac-operator/api/testdata/a fixture.json")
        self.commit()
        (self.repo / "osac-operator/api/go.mod").unlink()
        self.assert_changed(True, "osac-operator/api")
        self.assert_changed(False, "osac-metering/schema")

    def test_ignored_untracked_file_is_excluded(self) -> None:
        self.write(".gitignore", "**/ignored.json\n")
        self.commit()
        self.write("osac-operator/api/ignored.json")
        self.assert_changed(False, "osac-operator/api")

    def test_missing_base_selects_all_modules(self) -> None:
        self.git("symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
        self.git("update-ref", "-d", "refs/remotes/origin/main")
        self.assertEqual(self.selected("go-test", "go"), set(MODULES))
        self.assert_changed(True, "unrelated-path")

    def test_shallow_history_uses_base_ref_fallback(self) -> None:
        self.write("osac-operator/api/go.mod")
        self.commit()
        (self.repo / ".git/shallow").write_text(self.git("rev-parse", "HEAD") + "\n")
        self.assert_changed(True, "osac-operator/api")
        self.assertIn("fulfillment-service", self.selected("go-test", "go"))

    def test_ui_runs_full_sequence_for_tracked_and_untracked_inputs(self) -> None:
        for path in ("osac-ui/package.json", "osac-ui/pnpm-lock.yaml", "osac-ui/new.test.tsx"):
            with self.subTest(path=path):
                self.write(path)
                result = self.run_step("osac-ui")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    [record["args"] for record in self.records("pnpm")],
                    [["install", "--frozen-lockfile"], ["run", "typecheck"], ["lint"], ["test"]],
                )
                self.git("reset", "--hard", "HEAD")
                self.git("clean", "-fd")

    def test_e2e_collection_selection_and_flags(self) -> None:
        for path in ("tests/e2e/test_example.py", "tests/e2e/new_test.py", "pyproject.toml", "tests/__init__.py"):
            with self.subTest(path=path):
                self.write(path)
                result = self.run_step("e2e-collect")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    [record["args"] for record in self.records("python")],
                    [["-m", "pytest", "--collect-only", "-n", "0", "-m", "not metering", "tests/e2e/"]],
                )
                self.assertIn(
                    ["pip", "install", "--python", str(self.venv / "bin/python"), "-r", "pyproject.toml"],
                    [record["args"] for record in self.records("uv")],
                )
                self.git("reset", "--hard", "HEAD")
                self.git("clean", "-fd")

    def test_collection_failure_propagates(self) -> None:
        self.write("tests/e2e/test_example.py")
        self.assertNotEqual(self.run_step("e2e-collect", {"tool": "python"}).returncode, 0)

    def test_go_failure_aggregates_other_selected_modules(self) -> None:
        self.write("osac-metering/schema/go.mod")
        for step, failure in (
            (
                "go-test",
                {
                    "tool": "go",
                    "cwd": "osac-metering/metering-service",
                    "args": ["test", "-count=1", "example.test/osac-metering/metering-service/internal/unit"],
                },
            ),
            ("go-lint", {"tool": "make", "args": ["-C", "osac-metering/metering-service", "lint"]}),
        ):
            with self.subTest(step=step):
                result = self.run_step(step, failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertGreaterEqual(len(self.records()), 3)

    def test_api_failure_preserves_later_consumer_results(self) -> None:
        self.write("osac-operator/api/go.mod")
        failure = {"tool": "go", "cwd": "osac-operator/api", "args": ["test", "-count=1", "./..."]}
        self.assertNotEqual(self.run_step("go-test", failure).returncode, 0)
        self.assertTrue(any(record["cwd"] == "bare-metal-fulfillment-operator" for record in self.records("go")))

    def test_ui_dependency_failure_stops_validation(self) -> None:
        self.write("osac-ui/package.json")
        failure = {"tool": "pnpm", "args": ["install", "--frozen-lockfile"]}
        self.assertNotEqual(self.run_step("osac-ui", failure).returncode, 0)
        self.assertEqual([record["args"] for record in self.records("pnpm")], [failure["args"]])

    def test_package_discovery_failure_propagates(self) -> None:
        self.write("osac-metering/schema/go.mod")
        failure = {
            "tool": "go",
            "cwd": "osac-metering/schema",
            "args": ["list", "-f", "{{.ImportPath}} {{.Dir}}", "./..."],
        }
        self.assertNotEqual(self.run_step("go-test", failure).returncode, 0)

    def test_filtered_consumers_do_not_execute_container_or_envtest_packages(self) -> None:
        self.write("osac-metering/schema/go.mod")
        self.selected("go-test", "go")
        for record in self.records("go"):
            if record["args"][0] == "test":
                self.assertTrue(all("envtest" not in arg and "container" not in arg for arg in record["args"]))

    def test_empty_package_discovery_does_not_run_a_blank_package(self) -> None:
        self.write("osac-metering/schema/go.mod")
        self.env["GO_LIST_PACKAGES"] = "[]"
        result = self.run_step("go-test")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.count("no sandbox-supported packages"), 3)
        self.assertTrue(all(record["args"][0] == "list" for record in self.records("go")))

    def test_all_callers_source_actual_helper(self) -> None:
        callers = {"go-lint", "go-test", "crd-sync", "helm-lint", "python-lint", "ansible", "osac-ui", "e2e-collect"}
        for name in callers:
            with self.subTest(step=name):
                self.assertIn("source .agentic-ci/changed.sh", self.steps[name])
                self.assertNotIn("changed()", self.steps[name])
                result = self.run_step(name)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_ci_covers_overlay_and_smoke_test(self) -> None:
        filters = yaml.safe_load((ROOT / ".github/filters/ci-filters.yml").read_text())
        patterns = filters["component-hooks-smoke"]
        self.assertEqual(len(patterns), 1)
        for path in (".agentic-ci/**", "tools/test/agentic-ci-validation-smoke.py"):
            self.assertIn(path, patterns[0])
        workflow = yaml.safe_load((ROOT / ".github/workflows/component-hooks-smoke.yml").read_text())
        events = workflow.get("on", workflow.get(True))
        self.assertTrue({"pull_request", "merge_group"} <= events.keys())
        steps = workflow["jobs"]["run-component-hooks-smoke"]["steps"]
        self.assertTrue(any(step.get("uses") == "./.github/actions/setup-python" for step in steps))
        self.assertTrue(any("agentic-ci-validation-smoke.py" in step.get("run", "") for step in steps))


if __name__ == "__main__":
    unittest.main(verbosity=2)
