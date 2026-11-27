#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Framework-level tests for unwrap_list_envelope.

The helper unwraps the ``{count, next, previous, results}`` list envelope that
``BaseResource`` / ``BaseInfoResource`` apply via ``normalize_list_response``
when a resource sets ``response_normalizer = unwrap_list_envelope`` in its
schema_overrides entry. These tests exercise the shared helper
directly so the behavior is protected for every module that opts in.
"""

import sys
from pathlib import Path

import pytest

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.client import unwrap_list_envelope
from plugins.module_utils.vast.errors import VastAPIError

ENVELOPE = {
    "count": 2,
    "next": None,
    "previous": None,
    "results": [{"id": 1, "name": "a"}, {"id": 2, "name": "b"}],
}


class TestUnwrapListEnvelope:
    """The helper unwraps envelopes before filtering or *_info return."""

    def test_unwraps_envelope(self):
        assert unwrap_list_envelope([ENVELOPE]) == ENVELOPE["results"]

    def test_empty_list(self):
        assert unwrap_list_envelope([]) == []

    def test_non_envelope_raises(self):
        """A non-envelope element surfaces a typed VastAPIError, not a bare KeyError."""
        with pytest.raises(VastAPIError):
            unwrap_list_envelope([{"id": 1, "name": "a"}])

    def test_results_not_a_list_raises(self):
        """A 'results' that is not a list surfaces a typed VastAPIError."""
        with pytest.raises(VastAPIError):
            unwrap_list_envelope([{"count": 1, "results": {"id": 1}}])
