from __future__ import annotations

import re
from collections.abc import Callable

PROBE_EXIT_SENTINEL = "__OSAC_PROBE_EXIT_STATUS__"
_PROBE_EXIT_RE = re.compile(rf"(?m)^{PROBE_EXIT_SENTINEL}=(\d+)$")


def probe_connectivity(
    source: str,
    command: str,
    run_unchecked: Callable[[str, int], tuple[str, int]],
    *,
    transport: str,
    timeout: int = 30,
) -> bool:
    """Run a network probe and distinguish a failed probe from failed SSH transport."""
    wrapped_command = f'{command}; probe_rc=$?; printf \'\\n{PROBE_EXIT_SENTINEL}=%s\\n\' "$probe_rc"; exit "$probe_rc"'
    output, transport_rc = run_unchecked(wrapped_command, timeout)
    matches = _PROBE_EXIT_RE.findall(output)
    if len(matches) != 1:
        raise RuntimeError(
            f"{transport} connectivity probe from {source} did not complete "
            f"({transport.lower()}_rc={transport_rc}, output={output!r})"
        )

    probe_rc = int(matches[0])
    if transport_rc != probe_rc:
        raise RuntimeError(
            f"{transport} connectivity probe from {source} reported command exit {probe_rc}, "
            f"but {transport} exited {transport_rc} (output={output!r})"
        )
    if probe_rc not in (0, 1):
        raise RuntimeError(
            f"{transport} connectivity probe from {source} failed with unexpected command exit "
            f"{probe_rc} (output={output!r})"
        )
    return probe_rc == 0
