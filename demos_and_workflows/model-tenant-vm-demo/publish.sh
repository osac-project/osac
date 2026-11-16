#!/usr/bin/env bash
# Publish a Doc 3 demo cast: inject section markers if absent, upload to the
# local asciinema server (http://localhost:8080), and append the generated
# link to CAST-LINKS.md. Idempotent per cast file.
set -uo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cast="$1"
label="${2:-$(basename "${cast}" .cast)}"
links="${here}/CAST-LINKS.md"

# Headless asciinema recordings carry per-event delta timestamps; fix
# them before injecting markers (no-op for monotonic casts).
python3 -I "${here}/normalize-timestamps.py" "${cast}" || exit 1

# NB: match marker EVENTS only (`[ts, "m", ...]` at line start) — typed
# commands emit single-character "o" events, so a bare `"m"` grep matches
# every typed "m" and would skip marker injection entirely.
if ! grep -qE '^\[[0-9.]+, ?"m"' "${cast}"; then
  python3 -I "${here}/inject-markers.py" "${cast}" || exit 1
fi

if grep -q "(${cast##*/})" "${links}" 2>/dev/null; then
  echo "already published: ${cast##*/}"
  exit 0
fi

url=$(asciinema upload "${cast}" | grep -oE 'https?://[^[:space:]]+' | head -1) || { printf 'FAIL upload %s\n' "${cast}" >&2; exit 1; }
# The local server advertises a placeholder host; normalize to the local URL.
url="${url/asciinema.example.com/localhost:8080}"
printf '| %s | %s | %s |\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${label}" "[${cast##*/}](${url})" >> "${links}"
echo "published: ${label} -> ${url}"
