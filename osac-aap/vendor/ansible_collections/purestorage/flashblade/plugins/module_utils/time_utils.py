# -*- coding: utf-8 -*-

# Copyright: (c) 2024, Pure Storage Inc.
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Time conversion utilities for FlashBlade modules."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

from typing import Optional


class TimeConversionError(Exception):
    """Raised when time conversion fails."""

    pass


def time_to_milliseconds(time_str: str) -> int:
    """
    Convert various time formats to milliseconds.

    Supports:
    - 12-hour clock: '02AM', '2AM', '11PM', '12AM', '12PM'
    - Duration: '2w', '3d', '4h', '5m', '6s'

    Args:
        time_str: Time string to convert

    Returns:
        Milliseconds as integer

    Raises:
        TimeConversionError: If format is invalid

    Examples:
        >>> time_to_milliseconds('02AM')
        7200000
        >>> time_to_milliseconds('2d')
        172800000
    """
    if not time_str:
        raise TimeConversionError("Time string cannot be empty")

    # Try 12-hour clock format
    if len(time_str) >= 3 and time_str[-2:].upper() in ("AM", "PM"):
        return _clock_to_milliseconds(time_str)

    # Try duration format
    if len(time_str) >= 2 and time_str[-1].lower() in ("w", "d", "h", "m", "s"):
        return _duration_to_milliseconds(time_str)

    raise TimeConversionError(f"Invalid time format: {time_str}")


def _clock_to_milliseconds(hour_str: str) -> int:
    """Convert 12-hour clock to milliseconds from midnight."""
    try:
        time_part = int(hour_str[:-2])
        period = hour_str[-2:].upper()

        if not 1 <= time_part <= 12:
            raise TimeConversionError(f"Hour must be 1-12, got {time_part}")

        if period == "AM":
            return 0 if time_part == 12 else time_part * 3600000
        else:  # PM
            return 43200000 if time_part == 12 else (time_part + 12) * 3600000

    except (ValueError, IndexError) as e:
        raise TimeConversionError(f"Invalid clock format: {hour_str}") from e


def _duration_to_milliseconds(duration_str: str) -> int:
    """Convert duration string to milliseconds."""
    multipliers = {
        "w": 7 * 86400000,  # weeks
        "d": 86400000,  # days
        "h": 3600000,  # hours
        "m": 60000,  # minutes
        "s": 1000,  # seconds
    }

    try:
        unit = duration_str[-1].lower()
        number = int(duration_str[:-1])

        if unit not in multipliers:
            raise TimeConversionError(f"Invalid time unit: {unit}")

        if number < 0:
            raise TimeConversionError(f"Duration must be positive, got {number}")

        return number * multipliers[unit]

    except (ValueError, IndexError) as e:
        raise TimeConversionError(f"Invalid duration format: {duration_str}") from e


def milliseconds_to_time(millisecs: Optional[int]) -> Optional[str]:
    """
    Convert milliseconds to HH:MM time string.

    Args:
        millisecs: Milliseconds from midnight

    Returns:
        Time string in HH:MM format, or None if input is None/0

    Examples:
        >>> milliseconds_to_time(7200000)
        '02:00'
        >>> milliseconds_to_time(43200000)
        '12:00'
    """
    if not millisecs:
        return None

    hours = int(millisecs / 3600000) % 24
    minutes = int((millisecs % 3600000) / 60000)
    return f"{hours:02d}:{minutes:02d}"
