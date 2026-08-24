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
    install_protoc_gen_cleanapi()


def install_golangci_lint() -> None:
    """
    Installs the 'golangci-lint' tool.
    """
    install_tool(tool=tools.GOLANGCI_LINT)


def install_protoc_gen_cleanapi() -> None:
    """
    Installs the 'protoc-gen-cleanapi' tool.
    """
    install_tool(tool=tools.PROTOC_GEN_CLEANAPI)


def install_tool(tool: tools.Tool) -> None:
    """
    Installs the given tool.
    """
    # First check if it is already installed:
    if is_installed(tool):
        return

    # Log the installation start:
    logging.info(f"Installing version '{tool.version}' of '{tool.name}'")

    # Download and check the file that contains the checksums:
    artifact_checksum = verify_checksum(
        url=tool.checksums_url,
        expected_checksum=tool.checksums[tool.checksums_artifact],
        artifact_name=tool.compressed_artifact_name,
    )

    # Create a temporary directory for the downloaded files:
    tmp_dir = pathlib.Path(tempfile.mkdtemp())
    try:
        # Download the artifact and verify its checksum:
        artifact_name = tool.compressed_artifact_name
        download_artifact(url=tool.artifact_url, path=tmp_dir, filename=artifact_name)
        verify_downloaded_artifact(path=tmp_dir, filename=artifact_name, expected_checksum=artifact_checksum)

        # Extract the artifact:
        extract_artifact(artifact_path=str(tmp_dir / artifact_name), tmp_dir=str(tmp_dir))

        # Install the artifact:
        install_artifact(path=tmp_dir, extracted_name=tool.extracted_name, tool_name=tool.name)
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


def verify_checksum(url: str, expected_checksum: str, artifact_name: str) -> str:
    """
    Verifies the checksum of the given URL.
    """
    response = requests.get(url, timeout=30)
    response.raise_for_status()
    content = response.content
    actual_checksum = hashlib.sha256(content).hexdigest()
    if actual_checksum != expected_checksum:
        raise Exception(
            f"Failed to verify checksum of '{url}': "
            f"expected '{expected_checksum}', but got '{actual_checksum}'"
        )

    artifact_pattern = re.escape(artifact_name)
    pattern = fr"(?i)^(?P<checksum>[0-9a-fA-F]{{64}})\s+[*]?{artifact_pattern}$"
    text = content.decode("utf-8").replace("\r\n", "\n").replace("\r", "")
    artifact_match = re.search(
        pattern=pattern,
        string=text,
        flags=re.MULTILINE,
    )
    if artifact_match is None:
        raise Exception(f"Failed to find checksum for artifact '{artifact_name}' inside '{url}'")

    artifact_checksum = artifact_match.group("checksum")
    logging.info(f"Expected checksum for artifact '{artifact_name}' is '{artifact_checksum}'")

    return artifact_checksum


def download_artifact(url: str, path: pathlib.Path, filename: str) -> None:
    """
    Downloads the artifact from the given URL to the specified path.
    """
    commands.run(
        args=[
            "curl",
            "--connect-timeout", "10",
            "--max-time", "120",
            "--location",
            "--proto", "=https",
            "--silent",
            "--fail",
            "--output", str(path / filename),
            url,
        ],
        check=True,
    )


def verify_downloaded_artifact(path: pathlib.Path, filename: str, expected_checksum: str) -> None:
    """
    Verifies the checksum of the downloaded artifact.
    """
    file_path = path / filename
    sha256_hash = hashlib.sha256()
    with open(file=file_path, mode="rb") as file:
        for chunk in iter(lambda: file.read(4096), b""):
            sha256_hash.update(chunk)
    actual_checksum = sha256_hash.hexdigest()
    if actual_checksum != expected_checksum:
        raise ValueError(
            f"Checksum mismatch for {filename}: expected {expected_checksum}, got {actual_checksum}"
        )


def extract_artifact(artifact_path: str, tmp_dir: str) -> None:
    """
    Extracts the artifact from the given artifact path into the parent directory.
    """
    logging.info(f"Extracting artifact from '{artifact_path}'")
    commands.run(
        args=["tar", "--directory", tmp_dir, "--extract", "--file", artifact_path],
        check=True,
    )


def install_artifact(path: pathlib.Path, extracted_name: str, tool_name: str) -> None:
    """
    Installs the artifact from the given path.
    """
    bin_dir = dirs.bin()
    if not bin_dir.exists():
        bin_dir.mkdir(parents=True)
    extracted_bin = path / extracted_name
    bin_file = bin_dir / tool_name
    shutil.move(extracted_bin, bin_file)
    bin_stat = bin_file.stat()
    bin_file.chmod(bin_stat.st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    logging.info(f"Successfully installed '{tool_name}' to '{bin_file}'")
