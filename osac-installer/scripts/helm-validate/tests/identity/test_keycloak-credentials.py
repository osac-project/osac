#!/usr/bin/env python3
"""Run the Keycloak credential parameterization checks under pytest.

The checks render the chart and exercise the resolve-realm-secrets hook's sed
substitution against passwords with regex metacharacters in bash, so their
implementation stays in bash; this module only wires them into the suite.
"""

from __future__ import annotations

import pathlib
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[4]
SCRIPT = INSTALLER / "scripts/validate-keycloak-credentials.sh"


def test_main() -> None:
    subprocess.run(["bash", str(SCRIPT)], check=True)
