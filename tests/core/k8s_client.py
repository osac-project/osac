from __future__ import annotations

import json
import subprocess
from typing import Any

from tests.core.runner import run, run_unchecked


class K8sClient:
    def __init__(self, *, namespace: str, kubeconfig: str | None = None) -> None:
        self.namespace: str = namespace
        self.kubeconfig: str | None = kubeconfig

    def _base(self) -> list[str]:
        args: list[str] = ["kubectl"]
        if self.kubeconfig is not None:
            args.extend(["--kubeconfig", self.kubeconfig])
        args.extend(["--as", "system:admin"])
        return args

    def _get(self, *args: str, checked: bool = True) -> tuple[str, int]:
        if checked:
            return run(*self._base(), *args), 0
        return run_unchecked(*self._base(), *args)

    # Generic kubectl operations

    def get_json(self, *, resource: str, name: str) -> dict[str, Any]:
        return json.loads(run(*self._base(), "get", resource, name, "-n", self.namespace, "-o", "json"))

    def get_jsonpath(self, *, resource: str, name: str, jsonpath: str) -> str:
        return run(*self._base(), "get", resource, name, "-n", self.namespace, "-o", f"jsonpath={jsonpath}")

    def get_by_label(self, *, resource: str, label: str, jsonpath: str) -> str:
        return run(*self._base(), "get", resource, "-n", self.namespace, "-l", label, "-o", f"jsonpath={jsonpath}")

    def patch(self, *, resource: str, name: str, patch: str) -> tuple[str, int]:
        return run_unchecked(*self._base(), "patch", resource, name, "-n", self.namespace, "--type=merge", "-p", patch)

    def apply(self, *, manifest: str) -> None:
        args: list[str] = [*self._base(), "apply", "-f", "-"]
        subprocess.run(args, input=manifest, capture_output=True, text=True, check=True)

    def delete(self, *, resource: str, name: str, wait: bool = True) -> None:
        args = [*self._base(), "delete", resource, name, "-n", self.namespace]
        if not wait:
            args.append("--wait=false")
        run(*args, timeout=600)

    def is_present(self, *, resource: str, name: str) -> bool:
        _, rc = run_unchecked(*self._base(), "get", resource, name, "-n", self.namespace)
        return rc == 0

    def count_by_label_all_namespaces(self, *, resource: str, label: str) -> int:
        output: str = run(*self._base(), "get", resource, "-A", "-l", label, "--no-headers")
        if not output:
            return 0
        return len(output.strip().splitlines())

    # ComputeInstance queries

    def get_compute_instance_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "computeinstance",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/computeinstance-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_compute_instance_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "computeinstance", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    def get_compute_instance_last_restarted_at(self, *, name: str) -> str:
        return self.get_jsonpath(resource="computeinstance", name=name, jsonpath="{.status.lastRestartedAt}")

    def get_compute_instance_latest_job_id(self, *, name: str, job_type: str, checked: bool = True) -> str:
        output, rc = self._get("get", "computeinstance", name, "-n", self.namespace, "-o", "json", checked=checked)
        if rc != 0:
            return ""
        jobs: list[dict[str, Any]] = [
            j for j in json.loads(output).get("status", {}).get("jobs", []) if j["type"] == job_type
        ]
        if not jobs:
            return ""
        return sorted(jobs, key=lambda j: j["timestamp"], reverse=True)[0]["jobID"]

    def get_compute_instance_latest_job_state(self, *, name: str, job_type: str, checked: bool = True) -> str:
        output, rc = self._get("get", "computeinstance", name, "-n", self.namespace, "-o", "json", checked=checked)
        if rc != 0:
            return ""
        jobs: list[dict[str, Any]] = [
            j for j in json.loads(output).get("status", {}).get("jobs", []) if j["type"] == job_type
        ]
        if not jobs:
            return ""
        return sorted(jobs, key=lambda j: j["timestamp"], reverse=True)[0].get("state", "")

    def get_compute_instance_condition_status(self, *, name: str, condition_type: str, checked: bool = True) -> str:
        output, rc = self._get("get", "computeinstance", name, "-n", self.namespace, "-o", "json", checked=checked)
        if rc != 0:
            return ""
        conditions: list[dict[str, Any]] = json.loads(output).get("status", {}).get("conditions", [])
        for cond in conditions:
            if cond.get("type") == condition_type:
                return cond.get("status", "")
        return ""

    def get_compute_instance_vm_namespace(self, *, name: str) -> str:
        return self.get_jsonpath(
            resource="computeinstance", name=name, jsonpath="{.status.virtualMachineReference.namespace}"
        )

    # VirtualNetwork queries

    def get_virtual_network_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "virtualnetwork",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/virtualnetwork-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_virtual_network_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "virtualnetwork", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    # Subnet queries

    def get_subnet_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "subnet",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/subnet-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_subnet_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "subnet", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    # VirtualMachine/VMI queries (explicit namespace — may be on a different cluster)

    def get_vmi_creation_timestamp(self, *, vmi_namespace: str, compute_instance_name: str) -> str:
        return run(
            *self._base(),
            "get",
            "virtualmachineinstance",
            "-n",
            vmi_namespace,
            "-l",
            f"osac.openshift.io/computeinstance={compute_instance_name}",
            "-o",
            "jsonpath={.items[0].metadata.creationTimestamp}",
        )

    def get_vm_printable_status(self, *, name: str, vm_namespace: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "virtualmachine",
            name,
            "-n",
            vm_namespace,
            "-o",
            "jsonpath={.status.printableStatus}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_vm_run_strategy(self, *, name: str, vm_namespace: str) -> str:
        return run(
            *self._base(), "get", "virtualmachine", name, "-n", vm_namespace, "-o", "jsonpath={.spec.runStrategy}"
        )

    # PublicIPPool queries

    def patch_public_ip_pool_implementation_strategy(
        self, *, name: str, strategy: str = "metallb-l2"
    ) -> tuple[str, int]:
        return self.patch(
            resource="publicippool",
            name=name,
            patch=json.dumps({"spec": {"implementationStrategy": strategy}}),
        )

    def get_public_ip_pool_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "publicippool",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/publicippool-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_public_ip_pool_uuid(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "publicippool", name, "-n", self.namespace,
            "-o", "jsonpath={.metadata.labels.osac\\.openshift\\.io/publicippool-uuid}", checked=checked
        )
        return output if rc == 0 else ""

    def get_public_ip_pool_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "publicippool", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    # PublicIP queries

    def get_public_ip_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "publicip",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/publicip-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_public_ip_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "publicip", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    def get_public_ip_state(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "publicip", name, "-n", self.namespace, "-o", "jsonpath={.status.state}", checked=checked
        )
        return output if rc == 0 else ""

    # ClusterOrder queries

    def get_cluster_order_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "clusterorder",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/clusterorder-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_cluster_order_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "clusterorder", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""

    def get_cluster_order_latest_job_id(self, *, name: str, job_type: str, checked: bool = True) -> str:
        output, rc = self._get("get", "clusterorder", name, "-n", self.namespace, "-o", "json", checked=checked)
        if rc != 0:
            return ""
        jobs: list[dict[str, Any]] = [
            j for j in json.loads(output).get("status", {}).get("jobs", []) if j["type"] == job_type
        ]
        if not jobs:
            return ""
        return sorted(jobs, key=lambda j: j["timestamp"], reverse=True)[0]["jobID"]

    def get_cluster_order_latest_job_state(self, *, name: str, job_type: str, checked: bool = True) -> str:
        output, rc = self._get("get", "clusterorder", name, "-n", self.namespace, "-o", "json", checked=checked)
        if rc != 0:
            return ""
        jobs: list[dict[str, Any]] = [
            j for j in json.loads(output).get("status", {}).get("jobs", []) if j["type"] == job_type
        ]
        if not jobs:
            return ""
        return sorted(jobs, key=lambda j: j["timestamp"], reverse=True)[0].get("state", "")

    def get_cluster_order_hosted_cluster_name(self, *, name: str) -> str:
        return self.get_jsonpath(
            resource="clusterorder", name=name, jsonpath="{.status.clusterReference.hostedClusterName}"
        )

    def get_cluster_order_namespace(self, *, name: str) -> str:
        return self.get_jsonpath(resource="clusterorder", name=name, jsonpath="{.status.clusterReference.namespace}")

    def get_cluster_order_spec(self, *, name: str) -> dict[str, Any]:
        output = self.get_jsonpath(resource="clusterorder", name=name, jsonpath="{.spec}")
        return json.loads(output) if output else {}

    # SecurityGroup queries

    def get_security_group_name(self, *, uuid: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get",
            "securitygroup",
            "-n",
            self.namespace,
            "-l",
            f"osac.openshift.io/securitygroup-uuid={uuid}",
            "-o",
            "jsonpath={.items[0].metadata.name}",
            checked=checked,
        )
        return output if rc == 0 else ""

    def get_security_group_phase(self, *, name: str, checked: bool = True) -> str:
        output, rc = self._get(
            "get", "securitygroup", name, "-n", self.namespace, "-o", "jsonpath={.status.phase}", checked=checked
        )
        return output if rc == 0 else ""
