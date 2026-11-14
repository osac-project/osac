"""Exercise bootstrap rendering, completion policy and readiness without API access."""

import json
import os
import re
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[6]
PLAYBOOK = "/repo/tests/integration/caas/charts/bootstrap/tests/render.yml"


class BootstrapRenderingTest(unittest.TestCase):
    """Check production objects, bounded completion and fail-closed startup."""

    def run_bootstrap(
        self, variables: dict | None = None, tags: str = "bootstrap-render,bootstrap-configuration"
    ) -> subprocess.CompletedProcess[str]:
        """Run the selected local bootstrap tasks in the pinned, isolated EE."""
        variables = {"project_directory": "/repo", "aap_configuration_dispatcher_roles": [], **(variables or {})}
        image = os.environ.get("EE_IMAGE", "localhost/osac-aap:it")
        runtime = os.environ.get("CONTAINER_TOOL", "podman")
        # Read the checkout as its owner while preserving its SELinux labels.
        identity = ["--user", f"{os.getuid()}:{os.getgid()}"]
        if Path(runtime).name == "podman":
            identity += ["--userns=keep-id"]
        return subprocess.run(
            [
                runtime,
                "run",
                "--rm",
                "--pull=never",
                "--network=none",
                *identity,
                "--security-opt",
                "label=disable",
                "--volume",
                f"{ROOT}:/repo:ro",
                "--workdir",
                "/tmp",
                "--env",
                "HOME=/tmp",
                "--env",
                "PYTHONDONTWRITEBYTECODE=1",
                "--env",
                "ANSIBLE_NOCOLOR=1",
                "--env",
                "NAMESPACE=osac",
                "--env",
                "AAP_PASSWORD=offline-placeholder",
                "--env",
                f"AAP_EE_IMAGE={image}",
                "--env",
                "AAP_EE_IMAGE_PULL=never",
                "--env",
                "AAP_PROJECT_GIT_BRANCH=" + "a" * 40,
                "--entrypoint",
                "ansible-playbook",
                image,
                PLAYBOOK,
                "--tags",
                tags,
                "--extra-vars",
                json.dumps(variables),
            ],
            capture_output=True,
            text=True,
            timeout=90,
            check=False,
        )

    def test_renders_both_shared_groups_without_pod_rewrites(self) -> None:
        """Run the shared role and render its fulfillment Pods for Kubernetes."""
        result = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("TASK [Verify shared fulfillment Pod]", result.stdout)
        self.assertIn("Validate the pinned launch policy", result.stdout)
        self.assertIn("osac-bare-metal-fulfillment-ig", result.stdout)
        self.assertIn("osac-cluster-fulfillment-ig", result.stdout)

    def test_requires_a_ca_and_owned_service_ca_fixture(self) -> None:
        """Accept an owned fixture and reject missing CA data or foreign ConfigMaps."""
        reference = {"apiVersion": "v1", "kind": "ConfigMap", "name": "caas-it-configuration", "uid": "owner-uid"}
        ready = {
            "service_ca_source": {"resources": [{"data": {"ca.crt": "fixture CA"}}]},
            "service_ca_owner": {"resources": [{"metadata": {"uid": "owner-uid"}}]},
            "service_ca_existing": {"resources": []},
        }
        variations = [
            ("new fixture", {}, True),
            ("missing CA", {"service_ca_source": {"resources": []}}, False),
            ("missing CA key", {"service_ca_source": {"resources": [{"data": {}}]}}, False),
            ("empty CA", {"service_ca_source": {"resources": [{"data": {"ca.crt": " \n"}}]}}, False),
            ("missing owner", {"service_ca_owner": {"resources": []}}, False),
            ("missing namespace", {"service_ca_namespace": ""}, False),
        ]
        for name, owners, succeeds in [
            ("owned fixture", [reference], True),
            ("foreign fixture", [{**reference, "uid": "foreign-uid"}], False),
            ("unowned fixture", [], False),
            ("shared fixture", [reference, {**reference, "uid": "foreign-uid"}], False),
        ]:
            variations.append(
                (name, {"service_ca_existing": {"resources": [{"metadata": {"ownerReferences": owners}}]}}, succeeds)
            )
        for name, changed, succeeds in variations:
            with self.subTest(name=name):
                result = self.run_bootstrap({**ready, **changed}, "bootstrap-service-ca-validation")
                self.assertEqual(result.returncode == 0, succeeds, result.stdout + result.stderr)
                self.assertIn("TASK [Verify the service-CA source and ownership]", result.stdout)
                self.assertNotIn("TASK [Publish the service-CA fixture]", result.stdout)
                if not succeeds:
                    self.assertIn("foreign ConfigMaps are preserved", result.stdout)

    def test_rejects_no_matching_production_group(self) -> None:
        """Reject selections that yield zero production execution groups."""
        result = self.run_bootstrap({"selected_groups": ["missing-fulfillment"]})
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Expected exactly the selected production execution groups", result.stdout)

    def test_rejects_partial_production_group_selection(self) -> None:
        """Reject a missing requested group even when another group renders successfully."""
        result = self.run_bootstrap({"selected_groups": ["bare-metal-fulfillment", "missing-fulfillment"]})
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Expected exactly the selected production execution groups", result.stdout)

    def test_rejects_empty_requested_groups(self) -> None:
        """Keep bootstrap fail-closed when its requested selection is empty."""
        result = self.run_bootstrap({"selected_groups": []})
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Expected exactly the selected production execution groups", result.stdout)

    def test_runs_shared_launch_policy_validation(self) -> None:
        """Reject a template that violates the shared instance-group policy."""
        result = self.run_bootstrap({"controller_templates": [{
            "name": "osac-create-hosted-cluster",
            "playbook": "osac-aap/playbook_osac_create_hosted_cluster.yml",
            "inventory": "osac-cluster-fulfillment",
            "execution_environment": "osac-ee",
            "instance_groups": ["osac-cluster-fulfillment-ig"],
            "prevent_instance_group_fallback": False,
            "ask_variables_on_launch": True,
        }]})
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("must use the pinned execution", result.stdout)

    def test_rejects_a_broken_shared_inventory_path(self) -> None:
        """Expose an invalid source path without substituting another inventory."""
        result = self.run_bootstrap({"controller_inventory_sources": [{
            "name": "osac-cluster-fulfillment-is",
            "inventory": "osac-cluster-fulfillment",
            "source_path": "inventory/localhost.yml",
            "timeout": 120,
            "wait": True,
        }]})
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Shared inventory paths must resolve in the selected checkout", result.stdout)

    def test_configures_bounded_terminal_waits(self) -> None:
        """Keep native module deadlines inside their collectors and the whole hook."""
        result = self.run_bootstrap(tags="bootstrap-configuration")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("TASK [Verify completion policy]", result.stdout)
        budgets = re.findall(r"sync: timeout=(\d+)s, collector=(\d+)x(\d+)s", result.stdout)
        self.assertEqual(len(budgets), 2, result.stdout)
        collection_deadlines = []
        for deadline, attempts, interval in budgets:
            collection_deadline = int(attempts) * int(interval)
            self.assertGreater(collection_deadline, int(deadline))
            collection_deadlines.append(collection_deadline)
        self.assertLess(collection_deadlines[0] + 2 * collection_deadlines[1], 900)

    def test_requires_successful_project_and_inventory_syncs(self) -> None:
        """Reject pending, failed, canceled, incomplete and wrong-revision inputs."""
        sources = [{"name": "osac-bare-metal-fulfillment-is"}, {"name": "osac-cluster-fulfillment-is"}]
        ready = {
            "completed_projects": [{"status": "successful", "scm_revision": "a" * 40}],
            "controller_inventory_sources": sources,
            "completed_inventory_sources": [{**source, "status": "successful"} for source in sources],
        }
        variations = [
            ("ready", {}, True),
            ("missing project", {"completed_projects": []}, False),
            ("wrong revision", {"completed_projects": [{"status": "successful", "scm_revision": "b" * 40}]}, False),
            ("missing inventories", {"completed_inventory_sources": []}, False),
            ("partial inventories", {"completed_inventory_sources": ready["completed_inventory_sources"][:1]}, False),
        ]
        for status in ["pending", "running", "failed", "canceled", "error"]:
            variations.append(
                ("project " + status, {"completed_projects": [{"status": status, "scm_revision": "a" * 40}]}, False)
            )
            variations.append(
                (
                    "inventory " + status,
                    {"completed_inventory_sources": [{**source, "status": status} for source in sources]},
                    False,
                )
            )
        for name, changed, succeeds in variations:
            with self.subTest(name=name):
                result = self.run_bootstrap({**ready, **changed}, "bootstrap-ready")
                self.assertEqual(result.returncode == 0, succeeds, result.stdout + result.stderr)
                self.assertIn("TASK [Verify the completed project revision]", result.stdout)
                if not succeeds:
                    self.assertIn("must complete successfully", result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
