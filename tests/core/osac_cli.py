from __future__ import annotations

import re
from typing import Any

from tests.core.runner import run, run_unchecked


class OsacCLI:
    def __init__(self, *, binary: str, address: str, token_script: str, namespace: str) -> None:
        self.binary: str = binary
        self.namespace: str = namespace
        self._address: str = address
        self._token_script: str = token_script
        run(binary, "login", "--address", address, "--insecure", "--token-script", token_script)

    def relogin(self) -> None:
        run(self.binary, "login", "--address", self._address, "--insecure", "--token-script", self._token_script)

    @staticmethod
    def _parse_uuid(stdout: str) -> str:
        match: re.Match[str] | None = re.search(r"'([^']+)'", stdout)
        assert match is not None, f"Failed to parse UUID from CLI output: {stdout}"
        return match.group(1)

    def create_hub(self, *, hub_id: str, kubeconfig: str) -> None:
        run(self.binary, "create", "hub", "--id", hub_id, "--kubeconfig", kubeconfig, "--namespace", self.namespace)

    def create_compute_instance(
        self,
        *,
        template: str,
        network_attachments: list[dict[str, Any]] | None = None,
        cores: int = 2,
        memory_gib: int = 4,
        boot_disk_size: int = 20,
        image: str = "quay.io/containerdisks/fedora:latest",
        image_source_type: str = "registry",
        run_strategy: str = "Always",
        user_data_secret_ref: str | None = None,
    ) -> str:
        args: list[str] = [
            self.binary,
            "create",
            "computeinstance",
            "--template",
            template,
            "--cores",
            str(cores),
            "--memory-gib",
            str(memory_gib),
            "--boot-disk-size",
            str(boot_disk_size),
            "--image",
            image,
            "--image-source-type",
            image_source_type,
            "--run-strategy",
            run_strategy,
        ]

        # Add network attachments
        if network_attachments is not None:
            for idx, attachment in enumerate(network_attachments):
                subnet = attachment.get("subnet")
                if not subnet or not isinstance(subnet, str):
                    raise ValueError(f"network_attachments[{idx}]: 'subnet' must be a non-empty string, got {subnet!r}")

                security_groups = attachment.get("security_groups", [])
                if not isinstance(security_groups, list):
                    raise ValueError(f"network_attachments[{idx}]: 'security_groups' must be a list, got {type(security_groups).__name__}")

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

        return self._parse_uuid(run(*args))

    def delete_compute_instance(self, *, uuid: str) -> None:
        run(self.binary, "delete", "computeinstance", uuid)

    def create_cluster(
        self,
        *,
        template: str,
        name: str | None = None,
        pull_secret_file: str | None = None,
        ssh_public_key_file: str | None = None,
        template_parameters: dict[str, str] | None = None,
        template_parameter_files: dict[str, str] | None = None,
    ) -> str:
        args: list[str] = [self.binary, "create", "cluster", "--template", template]
        if name is not None:
            args.extend(["--name", name])
        if pull_secret_file is not None:
            args.extend(["--pull-secret-file", pull_secret_file])
        if ssh_public_key_file is not None:
            args.extend(["--ssh-public-key-file", ssh_public_key_file])
        if template_parameters is not None:
            for key, value in template_parameters.items():
                args.extend(["-p", f"{key}={value}"])
        if template_parameter_files is not None:
            for key, path in template_parameter_files.items():
                args.extend(["-f", f"{key}={path}"])

        return self._parse_uuid(run(*args))

    def get(self, resource: str, *, output: str | None = None) -> str:
        args: list[str] = [self.binary, "get", resource]
        if output is not None:
            args.extend(["-o", output])
        return run(*args)

    def get_cluster_credential(self, credential: str, *, uuid: str) -> str:
        return run(self.binary, "get", credential, uuid)

    def get_unchecked(self, resource: str) -> tuple[str, int]:
        return run_unchecked(self.binary, "get", resource)

    def create_cluster_with_catalog_item(self, *, catalog_item: str, name: str) -> str:
        return self._parse_uuid(run(self.binary, "create", "cluster", "--catalog-item", catalog_item, "--name", name))

    def create_compute_instance_with_catalog_item(self, *, catalog_item: str, subnet: str | None = None) -> str:
        args: list[str] = [self.binary, "create", "computeinstance", "--catalog-item", catalog_item]
        if subnet is not None:
            args.extend(["--network-attachment", f"subnet={subnet}"])
        return self._parse_uuid(run(*args))

    def delete_cluster(self, *, uuid: str) -> None:
        run(self.binary, "delete", "cluster", uuid)
