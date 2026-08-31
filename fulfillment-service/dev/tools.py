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

import platform

# Platform detection - computed once at module load time
_SYSTEM_OS = platform.system().lower()
_SYSTEM_ARCH = platform.machine().lower()

# Architecture name mapping from system names to Go-style names used in releases
_ARCH_MAP = {
    "x86_64": "amd64",
    "aarch64": "arm64",
    "arm64": "arm64",  # macOS reports as arm64
    "amd64": "amd64",  # Already in target format
}

class Tool:
    def __init__(
        self,
        name: str,
        version: str,
        version_pattern: str,
        checksums: dict[str, str],
        github_repo: str = "",
        version_command: list[str] | None = None,
        # Override defaults below only if tool doesn't follow standard patterns
        checksums_artifact: str = "",
        compressed_artifact_name: str = "",
        extracted_name: str = "",
        checksums_url: str = "",
        artifact_url: str = "",
    ):
        """
        Initialize a Tool definition.
        """
        # Validate required fields
        if not name:
            raise ValueError("Tool name is required")
        if not version:
            raise ValueError(f"Tool version is required for '{name}'")
        if not version_pattern:
            raise ValueError(f"Tool version_pattern is required for '{name}'")
        if not checksums:
            raise ValueError(f"Tool checksums are required for '{name}'")

        # Get normalized architecture name
        arch_name = _ARCH_MAP.get(_SYSTEM_ARCH)
        if arch_name is None:
            raise ValueError(
                f"Unsupported architecture '{_SYSTEM_ARCH}' for tool '{name}'. "
                f"Supported architectures: {', '.join(_ARCH_MAP.keys())}"
            )

        # Save basic metadata
        self.name = name
        self.version = version
        self.version_command = version_command if version_command is not None else [name, "version"]
        self.version_pattern = version_pattern
        self.sys_os = _SYSTEM_OS
        self.sys_arch = arch_name

        # Apply defaults for standard artifact naming patterns
        if not checksums_artifact:
            checksums_artifact = "{name}-{version}-checksums.txt"
        if not compressed_artifact_name:
            compressed_artifact_name = "{name}-{version}-{sys_os}-{sys_arch}.tar.gz"
        if not extracted_name:
            extracted_name = "{name}-{version}-{sys_os}-{sys_arch}/{name}"

        # Apply GitHub release URL pattern if github_repo is provided
        if github_repo and not checksums_url:
            checksums_url = f"https://github.com/{github_repo}/releases/download/v{{version}}/{{checksums_artifact}}"
        if github_repo and not artifact_url:
            artifact_url = f"https://github.com/{github_repo}/releases/download/v{{version}}/{{artifact_name}}"

        # Format all template strings
        self.checksums_artifact = checksums_artifact.format(name=name, version=version)
        self.compressed_artifact_name = compressed_artifact_name.format(
            name=name,
            version=version,
            sys_os=self.sys_os,
            sys_arch=self.sys_arch,
        )
        self.extracted_name = extracted_name.format(
            name=name,
            version=version,
            sys_os=self.sys_os,
            sys_arch=self.sys_arch,
        )
        self.checksums_url = checksums_url.format(
            version=version,
            checksums_artifact=self.checksums_artifact,
        ) if checksums_url else ""
        self.artifact_url = artifact_url.format(
            version=version,
            artifact_name=self.compressed_artifact_name,
        ) if artifact_url else ""

        if not self.checksums_url or not self.artifact_url:
            raise ValueError(
                f"Tool '{name}' requires 'github_repo', or explicit 'checksums_url' and 'artifact_url'"
            )
        # Process checksums dictionary (keys can reference name/version placeholders)
        self.checksums = {}
        for artifact_name, artifact_checksum in checksums.items():
            artifact_name = artifact_name.format(
                name=name,
                version=version,
            )
            self.checksums[artifact_name] = artifact_checksum

GOLANGCI_LINT = Tool(
    name="golangci-lint",
    version="2.12.2",
    github_repo="golangci/golangci-lint",
    version_pattern=r"^golangci-lint has version (?P<version>\S+) built",
    checksums={
        "golangci-lint-{version}-checksums.txt": "9accc7943a5b4be44416a7d4efa7efb3d18c7f1919d6581cc3536e185301a2d4",
    },
)

PROTOC_GEN_CLEANAPI = Tool(
    name="protoc-gen-cleanapi",
    version="0.0.12",
    github_repo="jhernand/protoc-gen-cleanapi",
    version_command=["protoc-gen-cleanapi", "--version"],
    version_pattern=r"^(?P<version>\S+)",
    checksums={
        "protoc-gen-cleanapi_{version}_checksums.txt":
        "eaf158c71951b1238d20a1e48cbf5fd4af536dff4f2e75d2296fbb31d1157094",
    },
    # Override defaults - this tool uses underscores and no version in artifact name
    checksums_artifact="protoc-gen-cleanapi_{version}_checksums.txt",
    compressed_artifact_name="protoc-gen-cleanapi_{sys_os}_{sys_arch}.tar.gz",
    extracted_name="protoc-gen-cleanapi",  # Binary extracted directly, not in versioned dir
)
