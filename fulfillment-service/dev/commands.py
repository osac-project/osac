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

"""
Funcions to run and evaluate commands.
"""

import logging
import os
import shlex
import subprocess

from . import dirs


def run(
    args: list[str],
    **kwargs,
) -> subprocess.CompletedProcess:
    """
    Runs the given command.
    """
    kwargs.setdefault("cwd", dirs.project())
    args = resolve(args)
    cmd = ' '.join(map(shlex.quote, args))
    logging.debug(f"Running command '{cmd}'")
    result = subprocess.run(args=args, **kwargs)
    logging.debug(f"Exit code of '{cmd}' is {result.returncode}")
    return result


def eval(
    args: list[str],
    **kwargs,
) -> tuple[int, str]:
    """
    Runs the given command and returns the exit code and the text that it writes to the standard output.
    """
    kwargs.setdefault("cwd", dirs.project())
    args = resolve(args)
    cmd = ' '.join(map(shlex.quote, args))
    logging.debug(f"Evaluating command '{cmd}'")
    result = subprocess.run(args=args, check=False, capture_output=True, **kwargs)
    code = result.returncode
    output = result.stdout.decode("utf-8").strip()
    logging.debug(f"Exit code of '{cmd}' is {code}")
    logging.debug(f"Output of '{cmd}' is '{output}'")
    return (code, output)


def resolve(
    args: list[str]
) -> list[str]:
    """
    Returns a copy of the argument list with the command replaced by the full path to the binary in the project's bin
    directory, if it exists there. Otherwise the arguments are returned unchanged, letting the system 'PATH' handle
    resolution.
    """
    bin_path = dirs.bin() / args[0]
    if bin_path.exists() and os.access(bin_path, os.X_OK):
        return [str(bin_path)] + args[1:]
    return args
