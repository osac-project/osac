"""Check the shared OpenShell sandbox detection.

Run: python3 tools/test/test_openshell_gating.py
"""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INCLUDE = ROOT / "tools/openshell.mk"

MAKEFILE = """include {include}
all:
\t@echo $(OPENSHELL_SANDBOX_ACTIVE)
"""


class OpenShellGatingTest(unittest.TestCase):
    def active(self, value):
        with tempfile.TemporaryDirectory() as temp:
            makefile = Path(temp) / "Makefile"
            makefile.write_text(MAKEFILE.format(include=INCLUDE))
            env = {k: v for k, v in os.environ.items() if k != "OPENSHELL_SANDBOX"}
            if value is not None:
                env["OPENSHELL_SANDBOX"] = value
            result = subprocess.run(
                ["make", "--no-print-directory", "-C", temp, "all"],
                env=env, capture_output=True, text=True, timeout=60,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def test_unset_is_not_in_a_sandbox(self):
        self.assertEqual(self.active(None), "false")

    def test_empty_is_not_in_a_sandbox(self):
        """`origin` used to treat an empty value as set; it is not in a sandbox."""
        self.assertEqual(self.active(""), "false")

    def test_whitespace_is_not_in_a_sandbox(self):
        self.assertEqual(self.active("   "), "false")

    def test_non_empty_is_in_a_sandbox(self):
        self.assertEqual(self.active("osac-carbonite"), "true")


if __name__ == "__main__":
    unittest.main()
