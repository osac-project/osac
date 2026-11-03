#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Unit tests for duration_utils."""

import sys
from pathlib import Path

import pytest

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.utils.duration import (  # noqa: E402
    Duration,
    _equivalent_duration,
    normalize_duration,
    normalize_frames,
)


@pytest.mark.parametrize(
    "value,expected",
    [
        ("3600", 3600),
        ("01:00:00", 3600),
        ("00:00:30", 30),
        ("0:00:30", 30),
        ("100:00:00", 100 * 3600),
        ("1h", 3600),
        ("30m", 1800),
        ("1.5h", 5400),
        ("500ms", 0.5),
        ("1d", 86400),
        ("1 00:00:00", 86400),
        ("0 01:00:00", 3600),
        ("2 03:04:05", 2 * 86400 + 3 * 3600 + 4 * 60 + 5),
        ("1 00:00:00.000000", 86400),
        (3600, 3600),
    ],
)
def test_duration_parsing(value, expected):
    assert Duration(value) == expected


def test_duration_equivalence_across_formats():
    assert Duration("3600") == Duration("01:00:00") == Duration("1h") == Duration("60m")


def test_duration_days_format_equivalence():
    assert Duration("1 00:00:00") == Duration("1d") == Duration("24:00:00")


@pytest.mark.parametrize(
    "a,b,expected",
    [
        ("3600", "01:00:00", True),
        ("01:00:00", "1h", True),
        ("30m", "1800", True),
        ("3600", "3601", False),
        ("3600", None, False),
        (None, "3600", False),
        ("bogus", "3600", False),
    ],
)
def test_equivalent_duration(a, b, expected):
    assert _equivalent_duration(a, b) is expected


def test_normalize_duration_returns_user_when_equivalent():
    assert normalize_duration("01:00:00", "3600") == "3600"


def test_normalize_duration_returns_api_when_different():
    assert normalize_duration("01:00:00", "7200") == "01:00:00"


def test_normalize_duration_returns_api_when_user_none():
    assert normalize_duration("01:00:00", None) == "01:00:00"


def test_normalize_frames_equivalent_durations_match_user():
    api = [{"every": "01:00:00", "keep-local": "24:00:00"}]
    user = [{"every": "1h", "keep-local": "1d"}]
    assert normalize_frames(api, user) == [{"every": "1h", "keep-local": "1d"}]


def test_normalize_frames_different_durations_keep_api():
    api = [{"every": "02:00:00"}]
    user = [{"every": "1h"}]
    assert normalize_frames(api, user) == [{"every": "02:00:00"}]


def test_normalize_frames_length_mismatch_returns_api():
    api = [{"every": "1h"}, {"every": "2h"}]
    user = [{"every": "1h"}]
    assert normalize_frames(api, user) == api


def test_normalize_frames_none_inputs():
    assert normalize_frames(None, [{"every": "1h"}]) is None
    assert normalize_frames([{"every": "1h"}], None) == [{"every": "1h"}]
