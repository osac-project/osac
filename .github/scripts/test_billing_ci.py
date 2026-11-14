"""Check billing CI scope and execute its real gateway setup with fake Docker."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/tests.yml").read_text())
VARIABLES = ("OSAC_IT_BILLING_STUB_LISTEN", "OSAC_IT_BILLING_STUB_URL")
FAKE_DOCKER = """#!/usr/bin/env python3
import os
import sys

assert sys.argv[1:] == ["network", "inspect", "kind", "--format", "{{json .IPAM.Config}}"]
print(os.environ["DOCKER_IPAM_CONFIG"])
if os.environ.get("DOCKER_FAIL"):
    sys.exit(17)
"""


class BillingCIWiringTests(unittest.TestCase):
    def setUp(self) -> None:
        self.steps = WORKFLOW["jobs"]["run"]["steps"]
        self.setup = next(step for step in self.steps if step.get("name") == "Configure the billing integration stub")
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        docker = self.directory / "docker"
        docker.write_text(FAKE_DOCKER.replace("#!/usr/bin/env python3", f"#!{sys.executable}"))
        docker.chmod(0o755)
        self.output = self.directory / "github-env"

    def test_only_fulfillment_leg_between_install_and_test(self) -> None:
        wired_steps = [step for step in self.steps if any(name in yaml.safe_dump(step) for name in VARIABLES)]
        self.assertEqual(wired_steps, [self.setup])
        self.assertEqual(self.setup["if"], "inputs.kind == 'it-fulfillment-service'")
        index = self.steps.index(self.setup)
        install, run = self.steps[index - 1], self.steps[index + 1]
        self.assertEqual(install["if"], self.setup["if"])
        self.assertEqual(run["if"], self.setup["if"])
        self.assertEqual(install["run"], "make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac")
        self.assertIn("make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment", run["run"])
        for name in VARIABLES:
            self.assertNotIn(name, yaml.safe_dump(WORKFLOW.get("env", {})))
            for job in WORKFLOW["jobs"].values():
                self.assertNotIn(name, yaml.safe_dump(job.get("env", {})))
        self.assertEqual(WORKFLOW["jobs"]["run"]["env"]["KIND_EXPERIMENTAL_PROVIDER"], "docker")

    def execute_setup(self, config: str, *, fail: bool = False) -> subprocess.CompletedProcess[str]:
        self.output.write_text("EXISTING=value\n")
        return subprocess.run(
            ["bash", "-euo", "pipefail", "-c", self.setup["run"]],
            cwd=self.directory,
            env={
                **os.environ,
                "PATH": f"{self.directory}:{os.environ['PATH']}",
                "GITHUB_ENV": str(self.output),
                "DOCKER_IPAM_CONFIG": config,
                "DOCKER_FAIL": "1" if fail else "",
            },
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )

    def test_selects_ipv4_gateway_and_appends_both_variables(self) -> None:
        for config, expected in (
            ([{"Gateway": "172.18.0.1"}], "172.18.0.1"),
            ([{"Gateway": "fc00:f853:ccd:e793::1"}, {"Gateway": "172.19.0.1"}], "172.19.0.1"),
            ([{}, {"Gateway": "172.20.0.1"}, {"Gateway": "172.21.0.1"}], "172.20.0.1"),
        ):
            with self.subTest(config=config):
                result = self.execute_setup(json.dumps(config))
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    self.output.read_text(),
                    "EXISTING=value\nOSAC_IT_BILLING_STUB_LISTEN=0.0.0.0:18443\n"
                    f"OSAC_IT_BILLING_STUB_URL=https://{expected}:18443\n",
                )

    def test_missing_or_invalid_gateway_fails_without_exporting(self) -> None:
        for config in (
            "[]",
            "null",
            "not-json",
            "[{}]",
            '[{"Gateway": ""}]',
            '[{"Gateway": "fc00:f853:ccd:e793::1"}]',
            '[{"Gateway": "999.18.0.1"}]',
            '[{"Gateway": "172.18.0.1\\nINJECTED=value"}]',
        ):
            with self.subTest(config=config):
                result = self.execute_setup(config)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.output.read_text(), "EXISTING=value\n")

    def test_inspect_failure_fails_without_exporting(self) -> None:
        result = self.execute_setup('[{"Gateway": "172.18.0.1"}]', fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.output.read_text(), "EXISTING=value\n")


if __name__ == "__main__":
    unittest.main()
