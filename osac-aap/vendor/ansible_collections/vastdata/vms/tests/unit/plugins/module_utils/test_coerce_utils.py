#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for shared type-coercion helpers."""

import sys
from pathlib import Path

import pytest

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.utils.coerce import (  # noqa: E402
    coerce,
    first_of_list,
    to_float,
    to_int,
    to_list,
    to_string,
)


@pytest.mark.parametrize(
    "value,expected",
    [
        (None, None),
        (42, 42),
        ("42", 42),
        ("200000", 200000),
        ("not-a-number", "not-a-number"),
        ([], []),
    ],
)
def test_to_int(value, expected):
    assert to_int(value) == expected


def test_as_field_normalizer_bridges_coercer_to_normalizer_signature():
    """field_normalizers are called as fn(api_value, user_value); the adapter
    must accept the ignored user value and delegate to the pure coercer."""
    from plugins.module_utils.vast.schema_overrides import as_field_normalizer  # noqa: E402

    normalizer = as_field_normalizer(to_int)
    assert normalizer("200000", 200000) == 200000
    assert normalizer(None, None) is None


@pytest.mark.parametrize(
    "value,expected",
    [
        (None, None),
        (42, "42"),
        ("x", "x"),
    ],
)
def test_to_string(value, expected):
    assert to_string(value) == expected


@pytest.mark.parametrize(
    "value,expected",
    [
        (None, None),
        (1.5, 1.5),
        ("1.5", 1.5),
        ("bad", "bad"),
    ],
)
def test_to_float(value, expected):
    assert to_float(value) == expected


@pytest.mark.parametrize(
    "value,expected",
    [
        (None, None),
        ([1, 2], [1, 2]),
        (1, [1]),
        ("x", ["x"]),
    ],
)
def test_to_list(value, expected):
    assert to_list(value) == expected


@pytest.mark.parametrize(
    "value,expected",
    [
        ([1, 2], 1),
        ([], None),
        (5, 5),
        (None, None),
    ],
)
def test_first_of_list(value, expected):
    assert first_of_list(value) == expected


@pytest.mark.parametrize(
    "fn,value,expected",
    [
        ("to_int", "7", 7),
        ("to_string", 7, "7"),
        ("to_float", "2.5", 2.5),
        ("to_list", 3, [3]),
        ("first_of_list", [9], 9),
        ("unknown", 1, 1),
    ],
)
def test_coerce_dispatcher(fn, value, expected):
    assert coerce(value, fn) == expected
