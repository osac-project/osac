from __future__ import annotations

import re
import subprocess


def assert_disabled_create_error(error: subprocess.CalledProcessError) -> None:
    """Require the complete disabled-create status without exposing command arguments."""
    output = "\n".join(part for part in (error.stderr, error.stdout) if part)
    assert re.fullmatch(
        r"\s*ERROR:[ \t]*\r?\n[ \t]*Code:[ \t]*FailedPrecondition[ \t]*\r?\n"
        r"[ \t]*Message:[ \t]*provider networking is disabled[ \t]*\s*",
        output,
    ), "Expected exactly the disabled-create gRPC status and message"
