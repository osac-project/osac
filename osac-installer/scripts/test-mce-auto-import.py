#!/usr/bin/env python3
"""Exercise MCE policy setup with an oc double; optionally use the real CLI image."""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

HOOKS = Path(__file__).resolve().parents[1] / "charts/osac-infra/files/hooks"
POLICY = "/config/acm-import-policy.yaml"

OC_STUB = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

path = Path(os.environ["MCE_TEST_STATE"])
state = json.loads(path.read_text())
args = sys.argv[1:]
state["calls"].append(args)
path.write_text(json.dumps(state))

if args[:2] == ["get", "csv"]:
    print("Succeeded" if "-o" in args else "multicluster-engine.v2.17")
elif args[:2] == ["get", "multiclusterengine"]:
    print("Available" if "-o" in args else "multiclusterengine")
elif args[:2] == ["get", "configmap"]:
    sys.exit(1)
elif args[0] == "apply" and "/config/acm-import-policy.yaml" in args and state["policy_error"]:
    print("Policy update denied", file=sys.stderr)
    sys.exit(1)
elif args[0] not in ("apply", "annotate", "wait"):
    sys.exit("Unexpected oc call: " + repr(args))
"""


class MceAutoImportTest(unittest.TestCase):
    def run_hook(self, *, policy_error: bool = False) -> tuple[subprocess.CompletedProcess, list[list[str]]]:
        with tempfile.TemporaryDirectory(prefix="mce-auto-import-") as directory:
            root = Path(directory)
            state_path = root / "state.json"
            state_path.write_text(json.dumps({"calls": [], "policy_error": policy_error}))
            oc = root / "oc"
            oc.write_text(OC_STUB)
            oc.chmod(0o755)
            # Never let a developer's jq hide a missing dependency in the hook image.
            jq = root / "jq"
            jq.write_text("#!/bin/sh\necho 'jq is unavailable in the hook image' >&2\nexit 127\n")
            jq.chmod(0o755)
            command = ["bash", str(HOOKS / "configure-mce.sh")]
            env = {**os.environ, "PATH": f"{root}:{os.environ['PATH']}", "MCE_TEST_STATE": str(state_path)}
            if image := os.environ.get("MCE_TEST_CONTAINER_IMAGE"):
                command = [
                    os.environ.get("CONTAINER_TOOL", "podman"),
                    "run",
                    "--rm",
                    "--pull=never",
                    "--network=none",
                    "--read-only",
                    "--tmpfs",
                    "/tmp",
                    "-v",
                    f"{HOOKS}:/scripts:ro,z",
                    "-v",
                    f"{root}:/test:rw,z",
                    "--env",
                    "PATH=/test:/usr/bin:/bin",
                    "--env",
                    "MCE_TEST_STATE=/test/state.json",
                    "--entrypoint",
                    "/bin/bash",
                    image,
                    "/scripts/configure-mce.sh",
                ]
            result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
            return result, json.loads(state_path.read_text())["calls"]

    def test_apply_only_the_rendered_policy(self) -> None:
        result, calls = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        policy_calls = [call for call in calls if POLICY in call]
        self.assertEqual(len(policy_calls), 1)
        self.assertEqual(
            policy_calls[0],
            [
                "apply",
                "--server-side",
                "--force-conflicts",
                "--field-manager=osac-acm-policy",
                "-n",
                "multicluster-engine",
                "-f",
                POLICY,
            ],
        )
        self.assertFalse(
            any(call[:2] == ["get", "deployments"] for call in calls),
            "AAP provisioning owns the effective-agent readiness check",
        )

    def test_policy_is_applied_after_mce_ready_and_before_agent_service_config(self) -> None:
        result, calls = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        ready = next(i for i, call in enumerate(calls) if call[:2] == ["get", "multiclusterengine"] and "-o" in call)
        policy = next(i for i, call in enumerate(calls) if POLICY in call)
        agent_config = next(i for i, call in enumerate(calls) if "/config/config.yaml" in call)
        self.assertLess(ready, policy)
        self.assertLess(policy, agent_config)

    def test_policy_api_failure_stops_before_agent_service_config(self) -> None:
        result, calls = self.run_hook(policy_error=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Policy update denied", result.stderr)
        self.assertFalse(any("/config/config.yaml" in call for call in calls))


if __name__ == "__main__":
    unittest.main()
