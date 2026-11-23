#!/usr/bin/env python3
"""Seed the deterministic AgentlessNet task-contract state."""

from __future__ import annotations

import sys
from pathlib import Path

COMMON = Path(__file__).resolve().parents[5] / "tests" / "integration" / "common"
sys.path.insert(0, str(COMMON))

from agentless_net_awx_fixture import (  # noqa: E402
    prepare_agentless_net_contract_state,
)

TENANT = "agentless-net-contract"


def prepare(state_path: Path) -> None:
    prepare_agentless_net_contract_state(state_path, TENANT)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: prepare_state.py STATE_FILE")
    prepare(Path(sys.argv[1]))
