# -*- coding: utf-8 -*-

# Copyright (c) 2024 Simon Dodsley, <simon@purestorage.com>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

"""
This module adds shared functions for the FlashArray modules
"""

import platform
import os
import re

from ansible.module_utils.common.process import get_bin_path
from ansible.module_utils.facts.utils import get_file_content


def _findstr(text, match):
    """Find first line in text containing match string.

    Args:
        text: Multi-line text to search
        match: String to search for

    Returns:
        First line containing match, or None if not found
    """
    for line in text.splitlines():
        if match in line:
            return line
    return None


def remove_duplicates(items):
    """Remove duplicates from a list while preserving order.

    Args:
        items: List that may contain duplicates

    Returns:
        List with duplicates removed, order preserved
    """
    return list(dict.fromkeys(items))


def get_error_message(response, default="Unknown error"):
    """Safely extract error message from API response.

    Safely retrieves the first error message from a FlashBlade API response
    object, avoiding IndexError if the errors list is empty or missing.

    Args:
        response: API response object with potential errors attribute
        default: Default message to return if no error found (default: 'Unknown error')

    Returns:
        str: Error message from response.errors[0].message, or default if unavailable

    Examples:
        >>> # Safe - handles empty errors list
        >>> res = blade.get_file_systems(names=['nonexistent'])
        >>> error_msg = get_error_message(res)
        >>> module.fail_json(msg=f"Failed to get filesystem: {error_msg}")
        >>>
        >>> # With custom default message
        >>> error_msg = get_error_message(res, "Operation failed")

    Note:
        This function prevents IndexError crashes that occur when accessing
        res.errors[0].message directly without checking if errors exist.
    """
    if hasattr(response, "errors") and response.errors:
        errors = list(response.errors)
        if errors:
            return errors[0].message
    return default


def get_filesystem(module, blade):
    """Get filesystem from FlashBlade.

    Retrieves a filesystem by name, with optional context support for
    multi-tenancy (API version 2.17+).

    Args:
        module: Ansible module object with params['name'] and optional params['context']
        blade: FlashBlade client object

    Returns:
        Filesystem object if found, None otherwise

    Example:
        >>> fs = get_filesystem(module, blade)
        >>> if fs:
        >>>     print(fs.name)
    """
    CONTEXT_API_VERSION = "2.17"

    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and module.params.get("context"):
        res = blade.get_file_systems(
            names=[module.params["name"]], context_names=[module.params["context"]]
        )
    else:
        res = blade.get_file_systems(names=[module.params["name"]])

    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def human_to_bytes(size):
    """Convert human-readable byte string to bytes.

    Converts strings with size suffixes (K, M, G, T, P, B) to integer bytes.

    Args:
        size: Human-readable size string (e.g., '2G', '30M', '512K')

    Returns:
        int: Number of bytes, or 0 if format is invalid

    Examples:
        >>> human_to_bytes('2G')
        2147483648
        >>> human_to_bytes('512M')
        536870912
        >>> human_to_bytes('1K')
        1024
    """
    bytes = size[:-1]
    unit = size[-1].upper()
    if bytes.isdigit():
        bytes = int(bytes)
        if unit == "P":
            bytes *= 1125899906842624
        elif unit == "T":
            bytes *= 1099511627776
        elif unit == "G":
            bytes *= 1073741824
        elif unit == "M":
            bytes *= 1048576
        elif unit == "K":
            bytes *= 1024
        elif unit == "B":
            bytes *= 1
        else:
            bytes = 0
    else:
        bytes = 0
    return bytes


def human_to_real(iops):
    """Convert human-readable IOPS string to real number.

    Converts strings with K (thousands) or M (millions) suffixes to integers.

    Args:
        iops: Human-readable IOPS string (e.g., '2K', '30M', '5000')

    Returns:
        int: Real number of IOPS, or 0 if format is invalid

    Examples:
        >>> human_to_real('2K')
        2000
        >>> human_to_real('30M')
        30000000
        >>> human_to_real('5000')
        5000
    """
    digit = iops[:-1]
    unit = iops[-1].upper()
    if unit.isdigit():
        digit = iops
    elif digit.isdigit():
        digit = int(digit)
        if unit == "M":
            digit *= 1000000
        elif unit == "K":
            digit *= 1000
        else:
            digit = 0
    else:
        digit = 0
    return digit


def get_local_tz(module, timezone="UTC"):
    """Get local timezone of the server running the module.

    Attempts to detect the system timezone using platform-specific methods.
    Falls back to UTC if detection fails or platform is unsupported.

    Supported platforms:
        - Linux (via timedatectl or /etc/timezone)
        - SunOS (via /etc/default/init)
        - Darwin/macOS (via systemsetup)
        - BSD variants (via /etc/timezone)
        - AIX 6.1+ (via /etc/environment)

    Args:
        module: Ansible module object for running commands and warnings
        timezone: Default timezone if detection fails (default: 'UTC')

    Returns:
        str: Timezone string (e.g., 'America/New_York', 'UTC')

    Note:
        Windows is not supported and will always return UTC.
        Linux has been tested; other operating systems should work but may
        fall back to UTC on failure.
    """
    if platform.system() == "Linux":
        timedatectl = get_bin_path("timedatectl")
        if timedatectl is not None:
            rcode, stdout, stderr = module.run_command(timedatectl)
            if rcode == 0 and stdout:
                line = _findstr(stdout, "Time zone")
                full_tz = line.split(":", 1)[1].rstrip()
                timezone = full_tz.split()[0]
                return timezone
            else:
                module.warn("Incorrect timedatectl output. Timezone will be set to UTC")
        else:
            if os.path.exists("/etc/timezone"):
                timezone = get_file_content("/etc/timezone")
            else:
                module.warn("Could not find /etc/timezone. Assuming UTC")

    elif platform.system() == "SunOS":
        if os.path.exists("/etc/default/init"):
            for line in get_file_content("/etc/default/init", "").splitlines():
                if line.startswith("TZ="):
                    timezone = line.split("=", 1)[1]
                    return timezone
        else:
            module.warn("Could not find /etc/default/init. Assuming UTC")

    elif re.match("^Darwin", platform.platform()):
        systemsetup = get_bin_path("systemsetup")
        if systemsetup is not None:
            rcode, stdout, stderr = module.execute(systemsetup, "-gettimezone")
            if rcode == 0 and stdout:
                timezone = stdout.split(":", 1)[1].lstrip()
            else:
                module.warn("Could not run systemsetup. Assuming UTC")
        else:
            module.warn("Could not find systemsetup. Assuming UTC")

    elif re.match("^(Free|Net|Open)BSD", platform.platform()):
        if os.path.exists("/etc/timezone"):
            timezone = get_file_content("/etc/timezone")
        else:
            module.warn("Could not find /etc/timezone. Assuming UTC")

    elif platform.system() == "AIX":
        aix_oslevel = int(platform.version() + platform.release())
        if aix_oslevel >= 61:
            if os.path.exists("/etc/environment"):
                for line in get_file_content("/etc/environment", "").splitlines():
                    if line.startswith("TZ="):
                        timezone = line.split("=", 1)[1]
                        return timezone
            else:
                module.warn("Could not find /etc/environment. Assuming UTC")
        else:
            module.warn(
                "Cannot determine timezone when AIX os level < 61. Assuming UTC"
            )
    else:
        module.warn("Could not find /etc/timezone. Assuming UTC")
    return timezone
