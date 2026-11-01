#!/usr/bin/env python3
"""Seed the AgentlessNet ExternalIPAttachment playbook fixture."""

from __future__ import annotations

import sys
from pathlib import Path

COMMON = Path(__file__).resolve().parents[3] / "common"
sys.path.insert(0, str(COMMON))

from agentless_net_state_fixture import prepare_provider_network_state  # noqa: E402

TENANT = "agentless-eia-test"


def prepare(state_path: Path) -> None:
    prepare_provider_network_state(state_path, TENANT, "attachment")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: prepare_state.py STATE_FILE")
    prepare(Path(sys.argv[1]))
