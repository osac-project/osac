# -*- coding: utf-8 -*-

#
# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
# the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.
#

import hashlib
import logging
import pathlib
import platform
import re
import shutil
import stat
import tempfile

import click
import requests

from . import commands
from . import dirs
from . import tools


@click.command()
def setup() -> None:
    """
    Prepares the development environment.
    """
    install_golangci_lint()


def install_golangci_lint() -> None:
    """
    Installs the 'golangci-lint' tool.
    """
    # First check if it is already installed:
    tool = tools.GOLANGCI_LINT
    if is_installed(tool):
        return

    # Download and check the file that contains the checksums:
    logging.info(f"Installing version '{tool.version}' of '{tool.name}'")
    checksums_artifact = f"golangci-lint-{tool.version}-checksums.txt"
    checksums_url = (
        f"https://github.com"
        f"/golangci/golangci-lint/releases/download/v{tool.version}"
        f"/{checksums_artifact}"
    )
    checksums_response = requests.get(checksums_url, timeout=30)
    checksums_response.raise_for_status()
    checksums_content = checksums_response.content
    expected_checksum = tool.checksums[checksums_artifact]
    actual_checksum = hashlib.sha256(checksums_content).hexdigest()
    if actual_checksum != expected_checksum:
        raise Exception(
            f"Failed to verify checksum of '{checksums_url}', expected '{expected_checksum}' "
            f"but got '{actual_checksum}'"
        )

    # Get the system and machine name, mapping to the Go-style architecture names used by golangci-lint releases:
    os_name = platform.system().lower()
    arch_name = platform.machine().lower()
    arch_map = {"x86_64": "amd64", "aarch64": "arm64"}
    arch_name = arch_map.get(arch_name, arch_name)

    # Extract the checksum of the artifact from the downloaded checksums:
    artifact_name = f"golangci-lint-{tool.version}-{os_name}-{arch_name}.tar.gz"
    artifact_pattern = re.escape(artifact_name)
    artifact_match = re.search(
        pattern=fr'^(?P<checksum>[0-9a-fA-F]+)\s+{artifact_pattern}$',
        string=checksums_content.decode("utf-8"),
        flags=re.MULTILINE,
    )
    if artifact_match is None:
        raise Exception(f"Failed to find checksum for artifact '{artifact_name}' inside '{checksums_url}'")
    expected_checksum = artifact_match.group("checksum")
    logging.info(f"Expected checksum for artifact '{artifact_name}' is '{expected_checksum}'")

    # Create a temporary directory for the downloaded files:
    tmp_dir = pathlib.Path(tempfile.mkdtemp())
    try:
        # Download the artifact and verify its checksum:
        artifact_url = (
            f"https://github.com"
            f"/golangci/golangci-lint/releases/download/v{tool.version}"
            f"/{artifact_name}"
        )
        artifact_file = tmp_dir / artifact_name
        commands.run(
            args=[
                "curl",
                "--connect-timeout", "10",
                "--max-time", "120",
                "--location",
                "--silent",
                "--fail",
                "--output", str(artifact_file),
                artifact_url,
            ],
            check=True,
        )
        checksum_file = tmp_dir / f"{artifact_name}.txt"
        with open(file=checksum_file, encoding="utf-8", mode="w") as file:
            file.write(f"{expected_checksum} {artifact_file}\n")
        commands.run(
            args=["sha256sum", "--check", str(checksum_file)],
            check=True,
        )

        # Extract the tarball and install the binary:
        commands.run(
            args=[
                "tar",
                "--directory", str(tmp_dir),
                "--extract",
                "--file", str(artifact_file),
            ],
            check=True,
        )
        bin_dir = dirs.bin()
        if not bin_dir.exists():
            bin_dir.mkdir(parents=True)
        extracted_name = f"golangci-lint-{tool.version}-{os_name}-{arch_name}"
        extracted_bin = tmp_dir / extracted_name / tool.name
        bin_file = bin_dir / tool.name
        shutil.move(extracted_bin, bin_file)
        bin_stat = bin_file.stat()
        bin_file.chmod(bin_stat.st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    finally:
        shutil.rmtree(tmp_dir)


def is_installed(tool: tools.Tool) -> bool:
    """
    Checks if the given tool is already installed. It first looks in the project's bin directory,
    then falls back to the system PATH.
    """
    # Check the project bin directory first, then the system path:
    bin_path = dirs.bin() / tool.name
    if bin_path.exists():
        installed_path = str(bin_path)
    else:
        installed_path = shutil.which(tool.name)
    if installed_path is None:
        return False

    # Build the version command using the resolved path so we check the right binary:
    version_command = [installed_path] + tool.version_command[1:]
    tool_code, tool_out = commands.eval(args=version_command)
    if tool_code != 0:
        raise Exception(f"Failed to find version of installed '{tool.name}'")
    version_match = re.search(
        pattern=tool.version_pattern,
        string=tool_out,
        flags=re.MULTILINE,
    )
    if version_match is None:
        raise Exception(f"Failed to find version of installed '{tool.name}'")
    installed_version = version_match.group("version")
    if installed_version == tool.version:
        logging.info(
            f"Version {tool.version} of '{tool.name}' is already installed at '{installed_path}'"
        )
        return True
    logging.info(
        f"Found '{tool.name}' already installed at '{installed_path}', but version is '{installed_version}' "
        f"instead of '{tool.version}'"
    )
    return False
