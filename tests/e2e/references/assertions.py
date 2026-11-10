from __future__ import annotations

import re
import subprocess


def assert_disabled_create_error(error: subprocess.CalledProcessError) -> None:
    """Require the complete disabled-create status without exposing command arguments."""
    output = "\n".join(part for part in (error.stderr, error.stdout) if part)
    codes = re.findall(r"^\s*Code:[ \t]*(.*?)[ \t]*$", output, re.MULTILINE)
    messages = re.findall(r"^\s*Message:[ \t]*(.*?)[ \t]*$", output, re.MULTILINE)
    assert codes == ["FailedPrecondition"], "Expected exactly one disabled-create gRPC status"
    assert messages == ["provider networking is disabled"], "Expected exactly the disabled-create gRPC message"
