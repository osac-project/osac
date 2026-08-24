from __future__ import annotations

import re
import shutil
import tempfile
from typing import Any

from tests.core.runner import run, run_unchecked


class OsacCLI:
    def __init__(
        self,
        *,
        binary: str,
        address: str,
        token_script: str,
        namespace: str,
        default_instance_type: str | None = None,
        default_disk_image: str | None = None,
        private: bool = False,
    ) -> None:
        self.binary: str = binary
        self.namespace: str = namespace
        self._address: str = address
        self._token_script: str = token_script
        self._private: bool = private
        self.default_instance_type: str | None = default_instance_type
        self.default_disk_image: str | None = default_disk_image
        # Each OsacCLI instance gets its own config directory so that parallel
        # xdist workers (or multiple CLI fixtures) don't overwrite each other's
        # login credentials via the shared ~/.config/osac/config.json.
        self._config_dir: str = tempfile.mkdtemp(prefix="osac-config-")
        login_args = ["login", "--address", address, "--insecure", "--token-script", token_script]
        if self._private:
            login_args.append("--private")
        self._run(*login_args)

    def close(self) -> None:
        shutil.rmtree(self._config_dir, ignore_errors=True)

    @property
    def config_dir(self) -> str:
        return self._config_dir

    def _run(self, *args: str, timeout: int = 300) -> str:
        return run(self.binary, "--config", self._config_dir, *args, timeout=timeout)

    def _run_unchecked(self, *args: str, timeout: int = 300) -> tuple[str, int]:
        return run_unchecked(self.binary, "--config", self._config_dir, *args, timeout=timeout)

    def relogin(self) -> None:
        login_args = ["login", "--address", self._address, "--insecure", "--token-script", self._token_script]
        if self._private:
            login_args.append("--private")
        self._run(*login_args)

    @staticmethod
    def _parse_uuid(stdout: str) -> str:
        match: re.Match[str] | None = re.search(r"'([^']+)'", stdout)
        assert match is not None, f"Failed to parse UUID from CLI output: {stdout}"
        return match.group(1)

    def create_hub(self, *, hub_id: str, kubeconfig: str) -> None:
        self._run("create", "hub", "--id", hub_id, "--kubeconfig", kubeconfig, "--namespace", self.namespace)

    def create_compute_instance(
        self,
        *,
        template: str,
        name: str | None = None,
        network_attachments: list[dict[str, Any]] | None = None,
        boot_disk_size: int = 20,
        disk_image: str | None = None,
        run_strategy: str = "Always",
        user_data_secret_ref: str | None = None,
        instance_type: str | None = None,
    ) -> str:
        args: list[str] = [
            "create",
            "computeinstance",
            "--template",
            template,
            "--boot-disk-size",
            str(boot_disk_size),
            "--run-strategy",
            run_strategy,
        ]
        if name is not None:
            args.extend(["--name", name])

        effective_instance_type = instance_type if instance_type is not None else self.default_instance_type
        if effective_instance_type is not None:
            args.extend(["--instance-type", effective_instance_type])
        else:
            raise ValueError("instance_type or default_instance_type must be set")

        effective_disk_image = disk_image if disk_image is not None else self.default_disk_image
        if effective_disk_image is not None:
            args.extend(["--disk-image", effective_disk_image])
        else:
            raise ValueError("disk_image or default_disk_image must be set")

        # Add network attachments
        if network_attachments is not None:
            for idx, attachment in enumerate(network_attachments):
                subnet = attachment.get("subnet")
                if not subnet or not isinstance(subnet, str):
                    raise ValueError(f"network_attachments[{idx}]: 'subnet' must be a non-empty string, got {subnet!r}")

                security_groups = attachment.get("security_groups", [])
                if not isinstance(security_groups, list):
                    raise ValueError(
                        f"network_attachments[{idx}]: 'security_groups' must be a list,"
                        f" got {type(security_groups).__name__}"
                    )

                if security_groups and not all(isinstance(sg, str) and sg for sg in security_groups):
                    raise ValueError(f"network_attachments[{idx}]: all security_groups must be non-empty strings")

                # Build network-attachment flag value
                # Format: subnet=<id>,security-groups=<sg1>,<sg2>
                parts = [f"subnet={subnet}"]
                if security_groups:
                    sg_list = ",".join(security_groups)
                    parts.append(f"security-groups={sg_list}")

                args.extend(["--network-attachment", ",".join(parts)])

        if user_data_secret_ref is not None:
            args.extend(["--user-data", user_data_secret_ref])

        return self._parse_uuid(self._run(*args))

    def delete_compute_instance(self, *, uuid: str) -> None:
        self._run("delete", "computeinstance", uuid)

    def create_instance_type(
        self,
        *,
        name: str,
        cores: int,
        memory_gib: int,
        description: str = "",
        gpu_pci_device_selector: str = "",
        gpu_resource_name: str = "",
        gpu_count: int = 0,
    ) -> str:
        args: list[str] = [
            "create",
            "instancetype",
            "--name",
            name,
            "--cores",
            str(cores),
            "--memory-gib",
            str(memory_gib),
        ]
        if description:
            args.extend(["--description", description])
        if gpu_pci_device_selector:
            args.extend(["--gpu-pci-device-selector", gpu_pci_device_selector])
        if gpu_resource_name:
            args.extend(["--gpu-resource-name", gpu_resource_name])
        if gpu_count:
            args.extend(["--gpu-count", str(gpu_count)])
        return self._parse_uuid(self._run(*args))

    def describe_instance_type(self, *, name: str) -> str:
        return self._run("describe", "instancetype", name)

    def delete_instance_type(self, *, name: str) -> None:
        self._run("delete", "instancetype", name)

    def create_cluster(
        self,
        *,
        template: str,
        name: str | None = None,
        pull_secret_file: str | None = None,
        ssh_public_key_file: str | None = None,
        version: str | None = None,
        template_parameters: dict[str, str] | None = None,
        template_parameter_files: dict[str, str] | None = None,
    ) -> str:
        args: list[str] = ["create", "cluster", "--template", template]
        if name is not None:
            args.extend(["--name", name])
        if pull_secret_file is not None:
            args.extend(["--pull-secret-file", pull_secret_file])
        if ssh_public_key_file is not None:
            args.extend(["--ssh-public-key-file", ssh_public_key_file])
        if version is not None:
            args.extend(["--version", version])
        if template_parameters is not None:
            for key, value in template_parameters.items():
                args.extend(["-p", f"{key}={value}"])
        if template_parameter_files is not None:
            for key, path in template_parameter_files.items():
                args.extend(["-f", f"{key}={path}"])

        return self._parse_uuid(self._run(*args))

    def get(self, resource: str, *, output: str | None = None) -> str:
        args: list[str] = ["get", resource]
        if output is not None:
            args.extend(["-o", output])
        return self._run(*args)

    def get_cluster_credential(self, credential: str, *, uuid: str) -> str:
        return self._run("get", credential, uuid)

    def get_unchecked(self, resource: str) -> tuple[str, int]:
        return self._run_unchecked("get", resource)

    def create_cluster_with_catalog_item(self, *, catalog_item: str, name: str, version: str | None = None) -> str:
        args = ["create", "cluster", "--catalog-item", catalog_item, "--name", name]
        if version is not None:
            args.extend(["--version", version])
        return self._parse_uuid(self._run(*args))

    def create_compute_instance_with_catalog_item(
        self, *, catalog_item: str, name: str | None = None, subnet: str | None = None
    ) -> str:
        args: list[str] = ["create", "computeinstance", "--catalog-item", catalog_item]
        if name is not None:
            args.extend(["--name", name])
        if subnet is not None:
            args.extend(["--network-attachment", f"subnet={subnet}"])
        return self._parse_uuid(self._run(*args))

    def scale_cluster(self, *, uuid: str, node_set: str, size: int) -> None:
        self._run("scale", "cluster", uuid, "--node-set", node_set, "--size", str(size))

    def delete_cluster(self, *, uuid: str) -> None:
        self._run("delete", "cluster", uuid)

    def create_baremetal_instance(
        self,
        *,
        name: str,
        catalog_item: str,
        ssh_key: str | None = None,
        user_data: str | None = None,
        network_attachments: list[str] | None = None,
        external_ip_attachment: bool = False,
    ) -> str:
        args: list[str] = ["create", "baremetalinstance", "--name", name, "--catalog-item", catalog_item]
        if ssh_key is not None:
            args.extend(["--ssh-key", ssh_key])
        if user_data is not None:
            args.extend(["--user-data", user_data])
        if external_ip_attachment:
            args.extend(["--external-ip-attachment"])
        for na in network_attachments or []:
            args.extend(["--network-attachment", na])
        return self._parse_uuid(self._run(*args))

    def describe_baremetal_instance(self, *, name: str) -> str:
        return self._run("describe", "baremetalinstance", name)

    def delete_baremetal_instance(self, *, uuid: str) -> None:
        self._run("delete", "baremetalinstance", uuid)
