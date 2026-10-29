"""Run ComputeInstance storage playbooks against an explicitly selected Kind API."""

import copy
import json
import os
import subprocess
import sys
import tempfile
import unittest
import uuid
from pathlib import Path

from kubernetes import client, config
from kubernetes.client.exceptions import ApiException

OSAC_AAP = Path(__file__).resolve().parents[5]
WORKLOAD_NAME = "disk-mapping-vm"
VOLUME_GROUP = "cdi.kubevirt.io"
VOLUME_VERSION = "v1beta1"


class StorageMappingTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.kubeconfig = os.environ.get("KUBECONFIG")
        if not cls.kubeconfig:
            raise RuntimeError(
                "Set KUBECONFIG explicitly to the integration test cluster"
            )
        config.load_kube_config(config_file=cls.kubeconfig)
        cls.core = client.CoreV1Api()
        cls.storage = client.StorageV1Api()
        cls.custom = client.CustomObjectsApi()

    def setUp(self) -> None:
        self.namespace = "disk-mapping-" + uuid.uuid4().hex[:12]
        self.classes = {
            tier: f"{self.namespace}-{tier}" for tier in ("fast", "balanced", "archive")
        }
        self.owned_classes = []
        self.core.create_namespace({"metadata": {"name": self.namespace}})
        self.addCleanup(self._cleanup)
        self.snapshots = {}
        for tier, name in self.classes.items():
            self.storage.create_storage_class(
                {
                    "apiVersion": "storage.k8s.io/v1",
                    "kind": "StorageClass",
                    "metadata": {
                        "name": name,
                        "labels": {"osac.openshift.io/storage-tier": tier},
                    },
                    "provisioner": "csi.trident.netapp.io",
                    "volumeBindingMode": "WaitForFirstConsumer",
                }
            )
            self.owned_classes.append(name)
            self.snapshots[name] = self.storage.read_storage_class(name).to_dict()

        self.resource = {
            "apiVersion": "osac.openshift.io/v1alpha1",
            "kind": "ComputeInstance",
            "metadata": {
                "name": WORKLOAD_NAME,
                "namespace": self.namespace,
                "annotations": {
                    "osac.openshift.io/tenant": self.namespace,
                    "osac.openshift.io/subnet-target-namespace": self.namespace,
                },
            },
            "spec": {
                "templateID": "osac.templates.ocp_virt_vm",
                "networkAttachments": [{"subnetRef": "fixture-subnet"}],
                "vcpus": 1,
                "memoryGiB": 2,
                "bootDisk": {"sizeGiB": 40, "storageTier": "fast"},
                "additionalDisks": [
                    {"sizeGiB": 100, "storageTier": "balanced"},
                    {"sizeGiB": 250, "storageTier": "archive"},
                ],
                # Synthetic prerequisites; this target tests StorageClass mapping.
                "diskVolumeIDs": {
                    "boot": "00000000-0000-4000-8000-000000000001",
                    "disk-1": "00000000-0000-4000-8000-000000000002",
                    "disk-2": "00000000-0000-4000-8000-000000000003",
                },
                "image": {
                    "sourceType": "registry",
                    "sourceRef": "quay.io/containerdisks/fedora:latest",
                },
                "runStrategy": "Halted",
            },
            "status": {
                "tenantReference": {"name": self.namespace, "namespace": self.namespace}
            },
        }
        self.custom.create_namespaced_custom_object(
            "osac.openshift.io",
            "v1alpha1",
            self.namespace,
            "computeinstances",
            self.resource,
        )

    def _cleanup(self) -> None:
        try:
            self.custom.patch_namespaced_custom_object(
                "osac.openshift.io",
                "v1alpha1",
                self.namespace,
                "computeinstances",
                WORKLOAD_NAME,
                {"metadata": {"finalizers": []}},
            )
        except ApiException as error:
            if error.status != 404:
                raise
        finally:
            self.core.delete_namespace(self.namespace)
            for name in self.owned_classes:
                try:
                    self.storage.delete_storage_class(name)
                except ApiException as error:
                    if error.status != 404:
                        raise

    def _mapping(self) -> list[dict[str, str]]:
        return [
            {"name": self.classes[tier], "tier": tier}
            for tier in ("archive", "fast", "balanced")
        ]

    def _run_playbook(
        self,
        mapping: list[dict[str, str]] | None,
        definitions: list[dict[str, str]] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        job = {"resource": copy.deepcopy(self.resource)}
        if mapping is not None:
            job["tenant_storage_classes"] = mapping
        if definitions is not None:
            job["storage_tier_definitions"] = definitions
        variables = {
            "osac_job_vars": job,
            "create_step_wait_annotate_override": {
                "name": "osac.workflows.workflow_helpers",
                "tasks_from": "noop.yml",
            },
            "compute_instance_label": "osac.openshift.io/computeinstance",
            "compute_instance_osac_finalizer": "osac.openshift.io/computeinstance-aap",
            "ansible_python_interpreter": sys.executable,
            "ansible_connection": "local",
            "ansible_become": False,
        }
        environment = os.environ.copy()
        environment.pop("OSAC_REMOTE_CLUSTER_KUBECONFIG", None)
        for key in list(environment):
            if key.startswith("K8S_AUTH_"):
                environment.pop(key)
        environment.update(
            {
                "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
                "ANSIBLE_COLLECTIONS_PATH": f"{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
                "ANSIBLE_FORCE_COLOR": "false",
                "ANSIBLE_STDOUT_CALLBACK": "default",
                "K8S_AUTH_KUBECONFIG": self.kubeconfig,
            }
        )
        with tempfile.TemporaryDirectory(prefix="osac-disk-mapping-") as directory:
            path = Path(directory) / "job.json"
            path.write_text(json.dumps(variables))
            return subprocess.run(
                [
                    "uv",
                    "run",
                    "ansible-playbook",
                    "-i",
                    "localhost,",
                    str(OSAC_AAP / "playbook_osac_create_compute_instance.yml"),
                    "-e",
                    f"@{path}",
                ],
                cwd=OSAC_AAP,
                env=environment,
                capture_output=True,
                text=True,
                timeout=120,
                check=False,
            )

    def _assert_shared_classes_unchanged(self) -> None:
        for name, original in self.snapshots.items():
            self.assertEqual(self.storage.read_storage_class(name).to_dict(), original)

    def _assert_no_disks(self) -> None:
        for group, version, plural in (
            (VOLUME_GROUP, VOLUME_VERSION, "datavolumes"),
            ("kubevirt.io", "v1", "virtualmachines"),
        ):
            objects = self.custom.list_namespaced_custom_object(
                group, version, self.namespace, plural
            )
            self.assertEqual(objects["items"], [], plural)
        self._assert_shared_classes_unchanged()

    def _assert_disk_specs(self, classes: list[str]) -> list[str]:
        expected = (("root-disk", "40Gi"), ("disk-1", "100Gi"), ("disk-2", "250Gi"))
        identities = []
        all_disks = self.custom.list_namespaced_custom_object(
            VOLUME_GROUP, VOLUME_VERSION, self.namespace, "datavolumes"
        )["items"]
        self.assertEqual(len(all_disks), 3)
        for (suffix, size), storage_class in zip(expected, classes, strict=True):
            disk = self.custom.get_namespaced_custom_object(
                VOLUME_GROUP,
                VOLUME_VERSION,
                self.namespace,
                "datavolumes",
                f"{WORKLOAD_NAME}-{suffix}",
            )
            self.assertEqual(disk["spec"]["pvc"]["storageClassName"], storage_class)
            self.assertEqual(
                disk["spec"]["pvc"]["resources"]["requests"]["storage"], size
            )
            self.assertEqual(disk["spec"]["pvc"]["accessModes"], ["ReadWriteOnce"])
            self.assertEqual(
                disk["metadata"]["labels"]["osac.openshift.io/computeinstance"],
                WORKLOAD_NAME,
            )
            self.assertEqual(disk["metadata"]["namespace"], self.namespace)
            if suffix == "root-disk":
                self.assertEqual(
                    disk["spec"]["source"],
                    {
                        "registry": {
                            "url": "docker://quay.io/containerdisks/fedora:latest"
                        }
                    },
                )
            else:
                self.assertEqual(disk["spec"]["source"], {"blank": {}})
            identities.append(disk["metadata"]["uid"])
        self._assert_shared_classes_unchanged()
        return identities

    def test_shared_mapping_and_retry_preserve_disk_specs(self) -> None:
        expected = [self.classes[tier] for tier in ("fast", "balanced", "archive")]
        original_ids = None
        for _ in range(2):
            result = self._run_playbook(self._mapping())
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("osac.service.storage_provider :", result.stdout)
            ids = self._assert_disk_specs(expected)
            if original_ids is not None:
                self.assertEqual(ids, original_ids)
            original_ids = ids

    def test_missing_and_empty_mapping_create_no_disks(self) -> None:
        for mapping in (None, []):
            with self.subTest(mapping=mapping):
                result = self._run_playbook(mapping)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("has no tenant_storage_classes", result.stdout)
                self._assert_no_disks()

    def test_missing_boot_or_later_tier_creates_no_disks(self) -> None:
        for tier in ("fast", "archive"):
            with self.subTest(tier=tier):
                result = self._run_playbook(
                    [entry for entry in self._mapping() if entry["tier"] != tier]
                )
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("is not available", result.stdout)
                self.assertIn(tier, result.stdout)
                self._assert_no_disks()

    def test_ambiguous_boot_or_later_tier_creates_no_disks(self) -> None:
        for tier in ("fast", "archive"):
            with self.subTest(tier=tier):
                mapping = [
                    *self._mapping(),
                    {"name": self.classes["balanced"], "tier": tier},
                ]
                result = self._run_playbook(mapping)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("multiple resolved StorageClasses", result.stdout)
                self._assert_no_disks()

    def test_osac_managed_jit_classes_extend_the_shared_mapping(self) -> None:
        managed_class = f"osac-{self.namespace}-balanced"
        self.owned_classes.append(managed_class)
        definitions = [
            {"name": "balanced", "provider": "lvms", "protocol": "block"},
            {
                "name": "not-requested",
                "provider": "unused-provider",
                "protocol": "block",
            },
        ]
        result = self._run_playbook(
            [entry for entry in self._mapping() if entry["tier"] != "balanced"],
            definitions,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("osac.service.storage_provider :", result.stdout)
        self.assertIn("osac.templates.lvms_storage :", result.stdout)
        managed = self.storage.read_storage_class(managed_class)
        self.assertEqual(managed.provisioner, "topolvm.io")
        self.assertEqual(
            managed.metadata.labels["osac.openshift.io/tenant"], self.namespace
        )
        self.assertEqual(
            managed.metadata.labels["osac.openshift.io/storage-tier"], "balanced"
        )
        original_ids = self._assert_disk_specs(
            [self.classes["fast"], managed_class, self.classes["archive"]]
        )
        result = self._run_playbook(
            [
                {
                    "name": (
                        managed_class if entry["tier"] == "balanced" else entry["name"]
                    ),
                    "tier": entry["tier"],
                }
                for entry in self._mapping()
            ],
            definitions,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("osac.service.storage_provider :", result.stdout)
        self.assertEqual(
            self._assert_disk_specs(
                [self.classes["fast"], managed_class, self.classes["archive"]]
            ),
            original_ids,
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
