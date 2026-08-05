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

import click

from . import commands
from . import dirs
from . import setup


@click.group(invoke_without_command=True)
@click.pass_context
def lint(ctx) -> None:
    """
    Runs the linters. When no sub-command is specified it runs all of them.
    """
    if ctx.invoked_subcommand is None:
        ctx.invoke(go)
        ctx.invoke(proto)


@lint.command()
def go() -> None:
    """
    Runs the Go linter.
    """
    logging.info("Running Go linter")
    setup.install_golangci_lint()
    commands.run(
        args=["golangci-lint", "run"],
        check=True,
    )


@lint.command()
def proto() -> None:
    """
    Runs the Protobuf linter.
    """
    project_dir = dirs.project()
    bin_dir = dirs.bin()
    bin_dir.mkdir(parents=True, exist_ok=True)
    plugin_file = bin_dir / "buf-plugin-osac-lint"

    logging.info("Building 'buf-plugin-osac-lint'")
    commands.run(
        args=[
            "go", "build",
            "-o", f"{plugin_file.relative_to(project_dir)}",
            "./cmd/buf-plugin-osac-lint",
        ],
        check=True,
    )

    logging.info("Running Protobuf linter")
    commands.run(
        args=["buf", "lint"],
        check=True,
    )
