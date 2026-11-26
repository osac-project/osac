#!/usr/bin/env python3
"""Run the Fulfillment client-credentials helper checks under pytest.

The checks source bash helper functions from lib.sh and the devstack chart's
fulfillment-auth.sh and drive the dev-full hook scripts through mocked
curl/kubectl binaries, so their implementation stays in bash; this module
only wires them into the pytest suite.
"""

from __future__ import annotations

import pathlib
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[3]
SCRIPT = INSTALLER / "scripts/test-fulfillment-auth.sh"


def test_main() -> None:
    subprocess.run(["bash", str(SCRIPT)], check=True)
