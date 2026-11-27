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
    if "-o" not in args:
        print("multiclusterengine")
    else:
        # Report a starting phase first: a hook that ignores readiness must not pass.
        phase_reads = state.get("mce_phase_reads", 0) + 1
        state["mce_phase_reads"] = phase_reads
        path.write_text(json.dumps(state))
        print("Available" if phase_reads >= 2 else "Progressing")
elif args[:2] == ["get", "configmap"]:
    sys.exit(1)
elif args[:3] == ["get", "addondeploymentconfig", "hypershift-addon-deploy-config"]:
    print(state["agent_namespace"])
elif args[:3] == ["get", "deployment", "hypershift-addon-agent"]:
    if state["agent_read_error"]:
        sys.exit("Agent read denied")
    reads = sum(call[:3] == args[:3] for call in state["calls"])
    snapshots = state["agent_rollouts"]
    print(snapshots[min(reads, len(snapshots)) - 1])
elif args[0] == "apply" and "/config/acm-import-policy.yaml" in args and state["policy_error"]:
    print("Policy update denied", file=sys.stderr)
    sys.exit(1)
elif args[0] not in ("apply", "annotate", "wait"):
    sys.exit("Unexpected oc call: " + repr(args))
"""


class MceAutoImportTest(unittest.TestCase):
    def run_hook(
        self,
        *,
        policy_error: bool = False,
        agent_rollouts: tuple[str, ...] = ("true|2|2|1|1|1|1",),
        agent_read_error: bool = False,
        agent_namespace: str = "",
    ) -> tuple[subprocess.CompletedProcess, list[list[str]]]:
        """Run the real hook against fixture API results without real-time sleeps."""
        with tempfile.TemporaryDirectory(prefix="mce-auto-import-") as directory:
            root = Path(directory)
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "calls": [],
                "policy_error": policy_error,
                "agent_rollouts": agent_rollouts,
                "agent_read_error": agent_read_error,
                "agent_namespace": agent_namespace,
            }))
            oc = root / "oc"
            oc.write_text(OC_STUB)
            oc.chmod(0o755)
            sleep = root / "sleep"
            sleep.write_text("#!/bin/sh\nexit 0\n")
            sleep.chmod(0o755)
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
            result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=90)
            return result, json.loads(state_path.read_text())["calls"]

    def test_apply_only_the_rendered_policy(self) -> None:
        """The hook applies only the import policy, with a dedicated field manager."""
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

    def test_policy_is_applied_after_mce_ready_and_before_agent_service_config(self) -> None:
        """The policy lands only after the observed Available phase."""
        result, calls = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        phase_reads = [i for i, call in enumerate(calls) if call[:2] == ["get", "multiclusterengine"] and "-o" in call]
        self.assertGreaterEqual(len(phase_reads), 2, "the hook must observe a starting phase before Available")
        available = phase_reads[-1]
        policy = next(i for i, call in enumerate(calls) if POLICY in call)
        agent_config = next(i for i, call in enumerate(calls) if "/config/config.yaml" in call)
        self.assertLess(available, policy)
        self.assertLess(policy, agent_config)

    def test_policy_api_failure_stops_before_agent_service_config(self) -> None:
        result, calls = self.run_hook(policy_error=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Policy update denied", result.stderr)
        policy_calls = [call for call in calls if POLICY in call]
        self.assertGreaterEqual(len(policy_calls), 2, "a transient rejection must be retried")
        self.assertFalse(any("/config/config.yaml" in call for call in calls))

    def test_hook_waits_for_effective_policy_rollout(self) -> None:
        result, calls = self.run_hook(
            agent_namespace="custom-agent-namespace",
            agent_rollouts=("", "false|2|2|1|1|1|1", "true|3|2|1|1|1|1", "true|3|3|1|2|1|1", "true|3|3|1|1|1|1"),
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        policy = next(i for i, call in enumerate(calls) if POLICY in call)
        reads = [i for i, call in enumerate(calls) if call[:3] == ["get", "deployment", "hypershift-addon-agent"]]
        self.assertEqual(len(reads), 5)
        self.assertLess(policy, reads[0])
        self.assertIn("custom-agent-namespace", calls[reads[0]])

    def test_missing_agent_does_not_pass_and_times_out(self) -> None:
        result, calls = self.run_hook(agent_rollouts=("",))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("did not roll out with DISABLE_AUTO_IMPORT=true", result.stderr)
        reads = [call for call in calls if call[:3] == ["get", "deployment", "hypershift-addon-agent"]]
        self.assertGreater(len(reads), 1, "the agent may not have been created yet")
        self.assertIn("open-cluster-management-agent-addon", reads[0])

    def test_agent_read_failure_is_not_retried_as_rollout_delay(self) -> None:
        result, calls = self.run_hook(agent_read_error=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Agent read denied", result.stderr)
        self.assertEqual(sum(call[:3] == ["get", "deployment", "hypershift-addon-agent"] for call in calls), 1)


if __name__ == "__main__":
    unittest.main()
