"""Put the parent tests directory on sys.path so network tests can use _render."""

from __future__ import annotations

import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
