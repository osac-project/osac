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

import logging
import sys

import click

from . import commands
from . import dirs


@click.group(invoke_without_command=True)
@click.pass_context
def build(ctx) -> None:
    """
    Builds the project artifacts. When no sub-command is specified it defaults to building the binaries.
    """
    if ctx.invoked_subcommand is None:
        ctx.invoke(binaries)


@build.command()
def binaries() -> None:
    """
    Builds the Go binaries for each sub-directory of the cmd directory.
    """
    project_dir = dirs.project()
    cmd_dir = project_dir / "cmd"
    bin_dir = project_dir / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    error_count = 0
    for cmd_subdir in sorted(cmd_dir.iterdir()):
        if not cmd_subdir.is_dir():
            continue
        bin_name = cmd_subdir.name
        bin_file = bin_dir / bin_name
        if bin_file.exists():
            bin_file.unlink()
        logging.info(f"Building binary '{bin_name}'")
        result = commands.run([
            "go", "build",
            "-o", f"{bin_file.relative_to(project_dir)}",
            f"./{cmd_subdir.relative_to(project_dir)}",
        ])
        if result.returncode != 0:
            if bin_file.exists():
                bin_file.unlink()
            logging.error(f"Failed to build binary '{bin_name}'")
            error_count += 1
    if error_count > 0:
        logging.error("Found errors while building binaries")
        sys.exit(1)


@build.command()
def images() -> None:
    """
    Builds the container image using podman.
    """
    logging.info("Building container image")
    result = commands.run([
        "podman", "build",
        ".",
    ])
    if result.returncode != 0:
        logging.error("Failed to build container image")
        sys.exit(1)
