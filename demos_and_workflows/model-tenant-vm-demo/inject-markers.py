#!/usr/bin/env python3 -I
"""Inject asciinema v3 marker events aligned with documentation sections.

Scans output ("o") events for the top-level section banners printed by the
segment scripts (lines like "§2. Git administrator publication contract" —
the numbered section titles of the guide being demonstrated; leading
indentation is tolerated), and inserts a zero-interval marker event
([0.0, "m", "<section title>"]) before the first event carrying each
distinct section banner. Sub-step banners ("§N — ...") and inline mentions
are deliberately not marked, so the cast's marker chapters map one-to-one
onto the documentation's section structure. Zero-interval insertion
preserves the recording's timing exactly.

Usage: inject-markers.py <cast-file>   (edits in place)
"""
import json
import re
import sys
import os
import tempfile

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
BANNER_RE = re.compile(r"^[ \t]*§\d+\.[^\r\n\x00]*", re.MULTILINE)


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: inject-markers.py <cast-file>", file=sys.stderr)
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

    out = [lines[0]]
    seen = set()
    markers = 0
    for line in lines[1:]:
        line = line.strip()
        if not line or line.startswith("#"):
            out.append(line)
            continue
        event = json.loads(line)
        if len(event) == 3 and event[1] == "o" and isinstance(event[2], str):
            stripped = ANSI_RE.sub("", event[2])
            for match in BANNER_RE.finditer(stripped):
                label = match.group(0).strip().rstrip("=").strip()
                if label and label not in seen:
                    seen.add(label)
                    out.append(json.dumps([0.0, "m", label]))
                    markers += 1
        out.append(line)

    dirname = os.path.dirname(os.path.abspath(path))
    fd, tmp = tempfile.mkstemp(dir=dirname, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write("\n".join(out) + "\n")
        os.replace(tmp, path)
    except BaseException:
        os.unlink(tmp)
        raise
    print(f"injected {markers} markers into {path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
