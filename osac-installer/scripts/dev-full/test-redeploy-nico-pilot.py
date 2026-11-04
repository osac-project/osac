#!/usr/bin/env python3
"""Check the Kind-only guard and release/image wiring without a cluster."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("redeploy-nico-pilot.sh")
REVISION = "0123456789abcdef0123456789abcdef01234567"


class RedeployNicoPilotTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.calls = self.root / "calls"
        self.calls.touch()
        kubeconfig = self.root / "kind.kubeconfig"
        kubeconfig.touch()

        stubs = {
            "git": f"""#!/bin/sh
echo "git $*" >> "$CALLS"
case "$*" in
  *'branch --show-current'*) echo no-issue/nico-bmaas-integration-audit ;;
  *'rev-parse HEAD'*) echo {REVISION} ;;
esac
""",
            "kubectl": """#!/bin/sh
echo "kubectl $*" >> "$CALLS"
if [ "$1" = config ]; then echo "$FAKE_CONTEXT"; exit 0; fi
if [ "$FAIL_BINDING" = yes ] && [ "$4" = configmap ]; then exit 1; fi
case "$*" in
  *'get deployments -l app.kubernetes.io/name=bare-metal-fulfillment-operator -o name'*)
    echo deployment.apps/osac-bare-metal-fulfillment-operator-controller-manager ;;
esac
""",
            "helm": "#!/bin/sh\necho \"helm $*\" >> \"$CALLS\"\n",
            "make": "#!/bin/sh\necho \"make $*\" >> \"$CALLS\"\n",
            "docker": "#!/bin/sh\necho \"docker $*\" >> \"$CALLS\"\n",
        }
        for name, content in stubs.items():
            path = self.bin / name
            path.write_text(content)
            path.chmod(0o755)

        self.env = os.environ.copy()
        self.env.update(
            PATH=f"{self.bin}:{self.env['PATH']}",
            CALLS=str(self.calls),
            KUBECONFIG=str(kubeconfig),
            NS="osac",
            KIND_CLUSTER_NAME="osac-dev",
            NICO_PILOT_GITHUB_REPO="vladikr/osac",
            NICO_PILOT_BRANCH="no-issue/nico-bmaas-integration-audit",
            NICO_PILOT_CONTAINER_TOOL="docker",
            FAKE_CONTEXT="kind-osac-dev",
            FAIL_BINDING="no",
        )

    def run_script(self):
        return subprocess.run(
            ["bash", str(SCRIPT)],
            env=self.env,
            text=True,
            capture_output=True,
            check=False,
        )

    def calls_text(self):
        return self.calls.read_text()

    def test_wrong_cluster_stops_before_fetch_or_deploy(self):
        self.env["FAKE_CONTEXT"] = "nico-hub"
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("expected kind-osac-dev", result.stderr)
        self.assertNotIn("git -C", self.calls_text())
        self.assertNotIn("make -C", self.calls_text())

    def test_missing_binding_stops_before_fetch_or_deploy(self):
        self.env["FAIL_BINDING"] = "yes"
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing NICo tenant bindings", result.stderr)
        self.assertNotIn("git -C", self.calls_text())
        self.assertNotIn("make -C", self.calls_text())

    def test_builds_commit_tagged_images_and_updates_both_releases(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls_text()
        self.assertEqual(calls.count("make -C"), 6, calls)
        self.assertIn("execution-environment-build IMG=ghcr.io/osac-project/osac-aap:0123456789ab", calls)
        self.assertIn("image-build IMG=ghcr.io/osac-project/bare-metal-fulfillment-operator:0123456789ab", calls)
        self.assertIn("install-devstack PLATFORM=kind PROFILE=dev-full", calls)
        self.assertIn("awx.project.controllerVarsUrl=https://raw.githubusercontent.com/vladikr/osac/" + REVISION, calls)
        self.assertIn("install-osac PLATFORM=kind PROFILE=dev-full", calls)
        self.assertIn("bmf.image.tag=0123456789ab", calls)
        self.assertIn("rollout restart deployment.apps/osac-bare-metal-fulfillment-operator-controller-manager", calls)
        self.assertIn("rollout status deployment.apps/osac-bare-metal-fulfillment-operator-controller-manager", calls)


if __name__ == "__main__":
    unittest.main()
