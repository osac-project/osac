"""Make ``coverage_report`` importable when tests run via ``unittest discover``.

``python3 -m unittest discover -s tools/coverage-report/tests`` puts only the
tests directory on ``sys.path``. This module adds the package root so the test
modules can import the code under test without requiring an install step.
"""

import os
import sys

PACKAGE_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

if PACKAGE_ROOT not in sys.path:
    sys.path.insert(0, PACKAGE_ROOT)
