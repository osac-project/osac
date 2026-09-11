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

import glob
import logging
import os
import pathlib
import re
import shutil
import sys

import click

from . import commands
from . import dirs
from . import setup
from . import tools


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


def _sync_cleanapi_version() -> None:
    """
    Updates buf.yaml and buf.gen.yaml to use the cleanapi version from tools.PROTOC_GEN_CLEANAPI.
    This ensures the version is only maintained in one place (dev/tools.py).
    Always runs `buf dep update` to sync buf.lock, regardless of whether YAML files changed.
    """
    cleanapi_version = f"v{tools.PROTOC_GEN_CLEANAPI.version}"
    # Buf config now lives in the top-level proto/ module, not here.
    proto_dir = dirs.proto()

    files_to_update = [
        proto_dir / "buf.yaml",
        proto_dir / "buf.gen.yaml",
    ]

    # Pattern to match cleanapi version in YAML files
    pattern = re.compile(r'(buf\.build/cleanapi/cleanapi):v[\d.]+')
    replacement = rf'\1:{cleanapi_version}'

    for file_path in files_to_update:
        if not file_path.exists():
            logging.warning(f"File not found: {file_path}")
            continue

        content = file_path.read_text()
        updated_content = pattern.sub(replacement, content)

        if content != updated_content:
            file_path.write_text(updated_content)
            logging.info(f"Updated cleanapi version to {cleanapi_version} in {file_path.name}")

    # Always run buf dep update to ensure buf.lock is in sync
    # This handles fresh checkouts and failed prior updates
    commands.run(args=["buf", "dep", "update"], cwd=proto_dir, check=True)


@build.command()
def sync_cleanapi_version() -> None:
    """
    Syncs the cleanapi version from dev/tools.py to buf.yaml and buf.gen.yaml.

    This command is automatically run during proto builds, but can be run manually
    to update the YAML files without generating code.
    """
    _sync_cleanapi_version()


@build.command()
def protos() -> None:
    """
    Generates public proto files from private proto files using protoc-gen-cleanapi,
    lints them with buf, and generates Go code.

    Requires protoc to be installed.
    """
    # Ensure dependencies are installed
    setup.install_protoc_gen_cleanapi()

    # Sync cleanapi version from tools.py to buf.yaml and buf.gen.yaml
    _sync_cleanapi_version()

    # Check if required tools are available
    if not shutil.which("protoc"):
        logging.error(
            "protoc is not installed. Install it with:\n"
            "  macOS: brew install protobuf\n"
            "  Ubuntu/Debian: apt-get install protobuf-compiler\n"
            "  Or download from: https://github.com/protocolbuffers/protobuf/releases"
        )
        sys.exit(1)

    if not shutil.which("buf"):
        logging.error(
            "buf is not installed. Install it with:\n"
            "  macOS: brew install bufbuild/buf/buf\n"
            "  Or download from: https://buf.build/docs/installation"
        )
        sys.exit(1)

    logging.info("Generating public proto from private proto")

    # Proto sources + buf config now live in the top-level proto/
    # module. All paths below are relative to it, and every subprocess runs
    # with cwd=proto_dir (commands.run otherwise defaults cwd to this project).
    proto_dir = dirs.proto()

    # Export buf dependencies to a local directory for protoc to use
    deps_dir = proto_dir / ".buf" / "deps"
    deps_dir.mkdir(parents=True, exist_ok=True)

    # Export each dependency
    # cleanapi version comes from tools.PROTOC_GEN_CLEANAPI
    cleanapi_version = f"v{tools.PROTOC_GEN_CLEANAPI.version}"
    for dep in ["buf.build/bufbuild/protovalidate",
        "buf.build/googleapis/googleapis",
        "buf.build/grpc-ecosystem/grpc-gateway",
        f"buf.build/cleanapi/cleanapi:{cleanapi_version}"]:
        dep_name = dep.split("/")[-1].split(":")[0]
        dep_path = deps_dir / dep_name
        if not dep_path.exists():
            logging.info(f"Exporting {dep}")
            commands.run(args=["buf", "export", dep, "--output", str(dep_path)], cwd=proto_dir, check=True)

    # Get all private proto files (use absolute paths then convert to relative)
    proto_pattern = proto_dir / "private" / "osac" / "private" / "v1" / "*.proto"
    proto_files_abs = glob.glob(str(proto_pattern))
    if not proto_files_abs:
        logging.error("No proto files found in proto/private/osac/private/v1/")
        sys.exit(1)

    # Convert to relative paths for protoc
    proto_files = [str(pathlib.Path(f).relative_to(proto_dir)) for f in proto_files_abs]

    # Set up environment with plugin in PATH
    bin_dir = dirs.bin()
    env = os.environ.copy()
    env["PATH"] = f"{bin_dir}:{env.get('PATH', '')}"

    # Call protoc with the cleanapi plugin
    # Include buf dependencies and proto/ (for cleanapi) in proto_path
    # Use private/ as the main source, not the module root, to avoid duplicate imports
    commands.run(
        args=[
            "protoc",
            "--proto_path=private",
            "--proto_path=.",  # for cleanapi/cleanapi.proto
            "--proto_path=.buf/deps/protovalidate",
            "--proto_path=.buf/deps/googleapis",
            "--proto_path=.buf/deps/grpc-gateway",
            "--proto_path=.buf/deps/cleanapi",
            f"--plugin=protoc-gen-cleanapi={bin_dir}/protoc-gen-cleanapi",
            "--cleanapi_out=public",
            "--cleanapi_opt=proto_root=private",  # Where to find original files
        ] + proto_files,
        cwd=proto_dir,
        env=env,
        check=True,
    )

    logging.info(f"Public proto generation complete - processed {len(proto_files)} files")
    # TODO: Add functions to lint all proto files and generate Go code.
    # Currently not added because all private proto files are not annotated with cleanapi yet.
