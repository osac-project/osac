# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for time_utils module."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import pytest
from plugins.module_utils.time_utils import (
    time_to_milliseconds,
    milliseconds_to_time,
    TimeConversionError,
)


class TestTimeToMilliseconds:
    """Tests for time_to_milliseconds function."""

    # 12-hour clock format tests
    def test_12hour_am_single_digit(self):
        """Test single-digit AM hours."""
        assert time_to_milliseconds("1AM") == 3600000
        assert time_to_milliseconds("2AM") == 7200000
        assert time_to_milliseconds("9AM") == 32400000

    def test_12hour_am_double_digit(self):
        """Test double-digit AM hours."""
        assert time_to_milliseconds("01AM") == 3600000
        assert time_to_milliseconds("02AM") == 7200000
        assert time_to_milliseconds("11AM") == 39600000

    def test_12hour_am_midnight(self):
        """Test 12AM (midnight) edge case."""
        assert time_to_milliseconds("12AM") == 0

    def test_12hour_pm_single_digit(self):
        """Test single-digit PM hours."""
        assert time_to_milliseconds("1PM") == 46800000
        assert time_to_milliseconds("2PM") == 50400000
        assert time_to_milliseconds("11PM") == 82800000

    def test_12hour_pm_double_digit(self):
        """Test double-digit PM hours."""
        assert time_to_milliseconds("01PM") == 46800000
        assert time_to_milliseconds("02PM") == 50400000

    def test_12hour_pm_noon(self):
        """Test 12PM (noon) edge case."""
        assert time_to_milliseconds("12PM") == 43200000

    def test_12hour_case_insensitive(self):
        """Test that AM/PM is case-insensitive."""
        assert time_to_milliseconds("2am") == 7200000
        assert time_to_milliseconds("2pm") == 50400000
        assert time_to_milliseconds("2Am") == 7200000
        assert time_to_milliseconds("2Pm") == 50400000

    # Duration format tests
    def test_duration_weeks(self):
        """Test week duration format."""
        assert time_to_milliseconds("1w") == 604800000
        assert time_to_milliseconds("2w") == 1209600000

    def test_duration_days(self):
        """Test day duration format."""
        assert time_to_milliseconds("1d") == 86400000
        assert time_to_milliseconds("3d") == 259200000
        assert time_to_milliseconds("7d") == 604800000

    def test_duration_hours(self):
        """Test hour duration format."""
        assert time_to_milliseconds("1h") == 3600000
        assert time_to_milliseconds("24h") == 86400000

    def test_duration_minutes(self):
        """Test minute duration format."""
        assert time_to_milliseconds("1m") == 60000
        assert time_to_milliseconds("60m") == 3600000

    def test_duration_seconds(self):
        """Test second duration format."""
        assert time_to_milliseconds("1s") == 1000
        assert time_to_milliseconds("60s") == 60000

    def test_duration_case_insensitive(self):
        """Test that duration units are case-insensitive."""
        assert time_to_milliseconds("2W") == 1209600000
        assert time_to_milliseconds("3D") == 259200000
        assert time_to_milliseconds("4H") == 14400000

    # Error cases
    def test_empty_string(self):
        """Test that empty string raises error."""
        with pytest.raises(TimeConversionError, match="cannot be empty"):
            time_to_milliseconds("")

    def test_invalid_format(self):
        """Test that invalid format raises error."""
        with pytest.raises(TimeConversionError, match="Invalid duration format"):
            time_to_milliseconds("invalid")
        with pytest.raises(TimeConversionError, match="Invalid time format"):
            time_to_milliseconds("123")

    def test_invalid_hour_range(self):
        """Test that hours outside 1-12 raise error."""
        with pytest.raises(TimeConversionError, match="Hour must be 1-12"):
            time_to_milliseconds("0AM")
        with pytest.raises(TimeConversionError, match="Hour must be 1-12"):
            time_to_milliseconds("13AM")
        with pytest.raises(TimeConversionError, match="Hour must be 1-12"):
            time_to_milliseconds("25PM")

    def test_invalid_duration_unit(self):
        """Test that invalid duration unit raises error."""
        with pytest.raises(TimeConversionError, match="Invalid time format"):
            time_to_milliseconds("5x")

    def test_negative_duration(self):
        """Test that negative duration raises error."""
        with pytest.raises(TimeConversionError, match="must be positive"):
            time_to_milliseconds("-5d")

    def test_non_numeric_duration(self):
        """Test that non-numeric duration raises error."""
        with pytest.raises(TimeConversionError, match="Invalid duration format"):
            time_to_milliseconds("abcd")

    def test_non_numeric_hour(self):
        """Test that non-numeric hour raises error."""
        with pytest.raises(TimeConversionError, match="Invalid clock format"):
            time_to_milliseconds("abcAM")


class TestMillisecondsToTime:
    """Tests for milliseconds_to_time function."""

    def test_midnight(self):
        """Test conversion of midnight (0 milliseconds)."""
        assert milliseconds_to_time(0) is None

    def test_none_input(self):
        """Test that None input returns None."""
        assert milliseconds_to_time(None) is None

    def test_early_morning(self):
        """Test early morning hours."""
        assert milliseconds_to_time(3600000) == "01:00"  # 1 AM
        assert milliseconds_to_time(7200000) == "02:00"  # 2 AM

    def test_noon(self):
        """Test noon."""
        assert milliseconds_to_time(43200000) == "12:00"

    def test_afternoon(self):
        """Test afternoon hours."""
        assert milliseconds_to_time(46800000) == "13:00"  # 1 PM
        assert milliseconds_to_time(50400000) == "14:00"  # 2 PM

    def test_evening(self):
        """Test evening hours."""
        assert milliseconds_to_time(82800000) == "23:00"  # 11 PM

    def test_with_minutes(self):
        """Test times with minutes."""
        assert milliseconds_to_time(3660000) == "01:01"  # 1:01 AM
        assert milliseconds_to_time(7230000) == "02:00"  # 2:00:30 AM (rounds down)
        assert milliseconds_to_time(46860000) == "13:01"  # 1:01 PM

    def test_24hour_wraparound(self):
        """Test that hours wrap around at 24."""
        assert milliseconds_to_time(86400000) == "00:00"  # 24 hours = 0:00
        assert milliseconds_to_time(90000000) == "01:00"  # 25 hours = 1:00
