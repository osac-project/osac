# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Tests for resilient asynchronous task polling."""

from unittest.mock import MagicMock, patch

import pytest
from ansible_collections.vastdata.vms.plugins.module_utils.vast.client import VastClient
from ansible_collections.vastdata.vms.plugins.module_utils.vast.errors import VastAPIError, VastTransportError


def test_wait_for_task_tolerates_transport_errors_longer_than_six_polls():
    """A VMS restart / HA failover (transport errors) is waited out until timeout."""
    client = VastClient.__new__(VastClient)
    client._get_task = MagicMock(side_effect=[VastTransportError("connection refused")] * 20 + [{"id": 15, "state": "COMPLETED"}])

    with patch("ansible_collections.vastdata.vms.plugins.module_utils.vast.client.time.sleep"):
        result = client.wait_for_task(15, timeout=3000, poll_interval=5)

    assert result["state"] == "COMPLETED"
    assert client._get_task.call_count == 21


def test_wait_for_task_fails_fast_on_api_errors():
    """API-level polling errors (e.g. task not found, 401) fail after six polls."""
    client = VastClient.__new__(VastClient)
    client._get_task = MagicMock(side_effect=VastAPIError("Task 15 not found"))

    with patch("ansible_collections.vastdata.vms.plugins.module_utils.vast.client.time.sleep"):
        with pytest.raises(VastAPIError, match="not found"):
            client.wait_for_task(15, timeout=3000, poll_interval=5)

    assert client._get_task.call_count == 6
