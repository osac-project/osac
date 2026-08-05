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


class Tool:
    def __init__(
        self,
        name: str,
        version: str = "",
        version_command: list[str] = [],
        version_pattern: str = "",
        checksums: dict[str, str] = {},
    ):
        # Save the values:
        self.name = name
        self.version = version
        self.version_command = version_command
        self.version_pattern = version_pattern

        # The keys of the checksums dictionary can reference the name or the version:
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
    version_command=["golangci-lint", "version"],
    version_pattern=r"^golangci-lint has version (?P<version>\S+) built",
    checksums={
        "golangci-lint-{version}-checksums.txt": "9accc7943a5b4be44416a7d4efa7efb3d18c7f1919d6581cc3536e185301a2d4",
    },
)
