#!/usr/bin/env python3
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
This script is a development tool for the project.
"""

import logging
import sys

import click

import dev


@click.group()
def cli():
    """
    Development tools.
    """

# Add the commands:
cli.add_command(dev.build)
cli.add_command(dev.lint)
cli.add_command(dev.setup)
cli.add_command(dev.update)

if __name__ == '__main__':
    # Configure logging:
    formatter = dev.Formatter()
    handler = logging.StreamHandler()
    handler.setFormatter(formatter)
    logging.root.handlers = [handler]
    logging.root.level = logging.DEBUG

    # Run the command:
    try:
        cli()
    except Exception as err:
        logging.error(err)
        sys.exit(1)
