#!/usr/bin/env python3 -I
"""Normalize asciinema v3 cast timestamps recorded in headless mode.

asciinema rec without a TTY ("headless mode") writes each event's
timestamp as the DELTA since the previous event instead of the elapsed
time since the recording started (verified experimentally: `echo A;
sleep 2; echo B; sleep 3; echo C` records as 0.004 / 2.002 / 3.002
instead of 0.004 / 2.006 / 5.008). Players interpret timestamps as
elapsed time, so such a cast plays back compressed to its longest
single gap.

This script rewrites the timestamps as cumulative sums, restoring the
real timing. Casts whose timestamps are already monotonic (TTY-recorded
or already normalized) are left untouched.

Usage: normalize-timestamps.py <cast-file>   (edits in place)
"""
import json
import os
import sys
import tempfile


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: normalize-timestamps.py <cast-file>", file=sys.stderr)
        return 2
    path = sys.argv[1]

    with open(path, "r", encoding="utf-8") as f:
        lines = f.read().splitlines()

    if not lines:
        print("empty cast file", file=sys.stderr)
        return 1
    header = json.loads(lines[0])
    if header.get("version") != 3:
        print(f"unsupported cast version: {header.get('version')}", file=sys.stderr)
        return 1

    events = []
    for line in lines[1:]:
        line = line.strip()
        if not line or line.startswith("#"):
            events.append((line, None))
            continue
        event = json.loads(line)
        events.append((line, event if isinstance(event, list) else None))

    stamps = [e[1][0] for e in events if e[1] is not None and len(e[1]) >= 2]
    if all(stamps[i] >= stamps[i - 1] - 1e-9 for i in range(1, len(stamps))):
        print(f"timestamps already monotonic: {path}")
        return 0

    out = [lines[0]]
    elapsed = 0.0
    for line, event in events:
        if event is None:
            out.append(line)
            continue
        elapsed += event[0]
        event[0] = round(elapsed, 3)
        out.append(json.dumps(event, ensure_ascii=False))

    dirname = os.path.dirname(os.path.abspath(path))
    fd, tmp = tempfile.mkstemp(dir=dirname, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write("\n".join(out) + "\n")
        os.replace(tmp, path)
    except BaseException:
        os.unlink(tmp)
        raise
    print(f"normalized timestamps (duration {elapsed:.1f}s): {path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
