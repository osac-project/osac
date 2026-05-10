from __future__ import annotations

import re

from tests.core.runner import run


class OsacCLI:
    def __init__(self, *, binary: str, address: str, token_script: str, namespace: str) -> None:
        self.binary: str = binary
        self.namespace: str = namespace
        run(binary, "login", "--address", address, "--insecure", "--token-script", token_script)

    def create_hub(self, *, hub_id: str, kubeconfig: str) -> None:
        run(self.binary, "create", "hub", "--id", hub_id, "--kubeconfig", kubeconfig, "--namespace", self.namespace)

    def create_compute_instance(
        self,
        *,
        template: str,
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
        if user_data_secret_ref is not None:
            args.extend(["--user-data", user_data_secret_ref])

        stdout: str = run(*args)
        match: re.Match[str] | None = re.search(r"'([^']+)'", stdout)
        assert match is not None, f"Failed to parse UUID from CLI output: {stdout}"
        return match.group(1)

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

        stdout: str = run(*args)
        match: re.Match[str] | None = re.search(r"'([^']+)'", stdout)
        assert match is not None, f"Failed to parse UUID from CLI output: {stdout}"
        return match.group(1)

    def delete_cluster(self, *, uuid: str) -> None:
        run(self.binary, "delete", "cluster", uuid)
