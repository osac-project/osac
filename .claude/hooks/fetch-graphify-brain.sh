#!/usr/bin/env bash
# SessionStart hook: pull the latest CI-published graphify knowledge graph
# into graphify-out/ so graphify's own CLAUDE.md directive + PreToolUse hook
# (installed separately via `graphify claude install`) have a current graph
# to read. See OSAC-4013/4015/4016/4017 for the design this implements.
#
# This script must NEVER block or fail session start -- every failure path
# below falls back (to existing local graphify-out/, or to plain cold
# exploration) and always exits 0. It also never triggers local generation
# (no `graphify hook install`/`--watch`) -- a developer's own commits must
# never clobber the CI-fetched, org-wide graph with an incomplete
# single-machine view.

set -uo pipefail

# Anchor to the project root regardless of the caller's cwd, so the
# settings.json hook command can stay a plain `bash .../fetch-graphify-brain.sh`
# -- consistent with the existing update-ai-context.sh hook -- rather than
# needing its own `cd` wrapper.
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "${PROJECT_DIR}" || { echo "[graphify-brain] Could not cd to project dir ${PROJECT_DIR} -- skipping." >&2; exit 0; }

REPO="osac-project/osac"
RELEASE_TAG="graph-latest"
GRAPH_DIR="graphify-out"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

warn() {
  echo "[graphify-brain] $*" >&2
}

fall_back_to_local() {
  local reason="$1"
  if [[ -f "${GRAPH_DIR}/metadata.json" ]] && command -v jq >/dev/null 2>&1; then
    local sha age_note
    sha="$(jq -r '.source_sha // "unknown"' "${GRAPH_DIR}/metadata.json" 2>/dev/null || echo unknown)"
    age_note=""
    if [[ -f "${GRAPH_DIR}/.last-fetch-at" ]]; then
      local last_fetch now age_h
      last_fetch="$(cat "${GRAPH_DIR}/.last-fetch-at" 2>/dev/null || echo 0)"
      now="$(date -u +%s)"
      age_h=$(( (now - last_fetch) / 3600 ))
      age_note=" (~${age_h}h old)"
    fi
    warn "${reason} -- falling back to existing local graph (source ${sha:0:12}${age_note}, possibly stale)."
  else
    warn "${reason} -- no local graph available either. Brain unavailable this session; cold exploration will proceed normally."
  fi
  exit 0
}

# --- Step 1: graphify binary distribution check (OSAC-4015 decision 3) ---
# Fail-open: a fresh machine without graphify installed should never see a
# confusing raw shell error, just a one-line note and normal cold
# exploration for that session.
if ! command -v graphify >/dev/null 2>&1; then
  warn "graphify not installed -- skipping brain fetch, cold exploration will proceed. Install with: uv tool install graphifyy (or: pipx install graphifyy). Note the package name is 'graphifyy' (double y); the CLI command itself is 'graphify'."
  exit 0
fi

if ! command -v gh >/dev/null 2>&1; then
  fall_back_to_local "gh CLI not available"
fi

if ! command -v jq >/dev/null 2>&1; then
  fall_back_to_local "jq not available (needed to validate the pulled bundle)"
fi

# --- Step 2: pull the latest published bundle. osac-project/osac is public
# so no token is required -- gh may still pick up an ambient local token if
# the developer is already logged in, but none is needed for this to work. ---
if ! gh release download "${RELEASE_TAG}" --repo "${REPO}" \
      --pattern 'graphify-bundle.tar.gz' --dir "${TMP_DIR}" >/dev/null 2>&1; then
  fall_back_to_local "Could not fetch latest graph bundle (network/404/rate-limit)"
fi

# Defense-in-depth path-traversal/symlink guard before extracting anything.
# Low severity in practice (exploiting this needs write access to the GH
# Release, the same trust boundary as pushing source code), but cheap to
# check: reject absolute paths, '..' components, and symlink/hardlink
# entries rather than trusting GNU tar's leading-'/' stripping alone.
if ! tar tzf "${TMP_DIR}/graphify-bundle.tar.gz" > "${TMP_DIR}/members.txt" 2>/dev/null; then
  fall_back_to_local "Downloaded bundle is corrupt (failed to list contents)"
fi
if grep -qE '^/|(^|/)\.\.(/|$)' "${TMP_DIR}/members.txt"; then
  fall_back_to_local "Downloaded bundle contains unsafe path entries (path traversal guard)"
fi
# GNU tar's verbose listing leads each line with a type character (like
# `ls -l`): '-' regular, 'l' symlink, 'h' hardlink (verified directly:
# `l` and `h` are real leading characters, not just human-readable "->"/
# "link to" text) -- match on that instead of the arrow/text, which could
# false-positive on a filename that happens to contain those substrings.
if tar tvzf "${TMP_DIR}/graphify-bundle.tar.gz" 2>/dev/null | grep -qE '^[lh]'; then
  fall_back_to_local "Downloaded bundle contains symlink/hardlink entries (rejected for safety)"
fi

mkdir -p "${TMP_DIR}/extracted"
if ! tar xzf "${TMP_DIR}/graphify-bundle.tar.gz" -C "${TMP_DIR}/extracted" 2>/dev/null; then
  fall_back_to_local "Downloaded bundle is corrupt (failed to extract)"
fi

# --- Step 3: validate graph.json AND metadata.json before touching local state ---
GRAPH_JSON="${TMP_DIR}/extracted/graph.json"
META_JSON="${TMP_DIR}/extracted/metadata.json"

if [[ ! -f "${GRAPH_JSON}" ]] || ! jq -e 'has("nodes") and has("links")' "${GRAPH_JSON}" >/dev/null 2>&1; then
  fall_back_to_local "Downloaded graph.json is missing or malformed"
fi

if [[ ! -f "${META_JSON}" ]] || ! jq -e 'has("source_sha") and has("graphify_version") and has("generated_at")' "${META_JSON}" >/dev/null 2>&1; then
  fall_back_to_local "Downloaded metadata.json is missing or malformed (missing required fields)"
fi

BUNDLE_SHA="$(jq -r '.source_sha' "${META_JSON}")"
BUNDLE_GRAPHIFY_VERSION="$(jq -r '.graphify_version' "${META_JSON}")"

# --- Step 4: staleness check (signal only, never a hard rejection) ---
if git rev-parse --is-inside-work-tree >/dev/null 2>&1 && \
   git cat-file -e "${BUNDLE_SHA}" 2>/dev/null; then
  if git merge-base --is-ancestor "${BUNDLE_SHA}" HEAD 2>/dev/null; then
    behind_count="$(git rev-list --count "${BUNDLE_SHA}..HEAD" 2>/dev/null || echo 0)"
    if [[ "${behind_count}" -gt 0 ]]; then
      warn "Note: published graph is ${behind_count} commit(s) behind local HEAD -- it doesn't yet reflect your most recent local commits."
    fi
  else
    warn "Note: published graph's source (${BUNDLE_SHA:0:12}) is not an ancestor of local HEAD -- local checkout may be on a branch ahead of or diverged from what generated this graph."
  fi
else
  warn "Note: could not compare graph source SHA against local HEAD (shallow clone or unrelated history) -- staleness unknown, proceeding on TTL basis only."
fi

# --- Step 5: version check ---
# graphify --version's output includes non-numeric text (e.g. "graphify
# 0.9.41"); stripping whitespace alone leaves "graphify0.9.41", which would
# never equal metadata.json's bare-number graphify_version and would refuse
# every fetch. Extract just the numeric version on both sides (see the same
# fix in the graph-refresh workflow that writes graphify_version).
LOCAL_GRAPHIFY_VERSION="$(graphify --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
if [[ -n "${LOCAL_GRAPHIFY_VERSION}" && "${LOCAL_GRAPHIFY_VERSION}" != "${BUNDLE_GRAPHIFY_VERSION}" ]]; then
  # Direction matters: a local install *older* than what generated the graph
  # is a real compatibility risk worth refusing over. A local install
  # *newer* than the bundle isn't -- telling that developer to "upgrade"
  # would be backwards. `sort -V` (real version-aware ordering, not a
  # hand-rolled semver comparator) decides which case this is.
  newest="$(printf '%s\n%s\n' "${LOCAL_GRAPHIFY_VERSION}" "${BUNDLE_GRAPHIFY_VERSION}" | sort -V | tail -1)"
  if [[ "${newest}" == "${BUNDLE_GRAPHIFY_VERSION}" ]]; then
    warn "graphify version mismatch: published graph was generated with ${BUNDLE_GRAPHIFY_VERSION}, local install (${LOCAL_GRAPHIFY_VERSION}) is older. Refusing to load -- upgrade with: uv tool install --upgrade graphifyy (or: pipx upgrade graphifyy)."
    fall_back_to_local "Version mismatch"
  else
    warn "Note: local graphify (${LOCAL_GRAPHIFY_VERSION}) is newer than the version that generated the published graph (${BUNDLE_GRAPHIFY_VERSION}) -- loading anyway, no action needed."
  fi
fi

# --- Step 6: atomic swap ---
# Rename-based, never delete-before-replace: at every point in time either
# the old graphify-out/ or the new one exists on disk. If this script is
# killed (e.g. by its own hook timeout) between the two `mv`s, the old graph
# is still there under GRAPH_DIR -- there is no window where GRAPH_DIR is
# simply missing.
date -u +%s > "${TMP_DIR}/extracted/.last-fetch-at"
rm -rf "${GRAPH_DIR}.tmp" "${GRAPH_DIR}.old"
mv "${TMP_DIR}/extracted" "${GRAPH_DIR}.tmp"
if [[ -d "${GRAPH_DIR}" ]]; then
  mv "${GRAPH_DIR}" "${GRAPH_DIR}.old"
fi
mv "${GRAPH_DIR}.tmp" "${GRAPH_DIR}"
rm -rf "${GRAPH_DIR}.old"

warn "Fetched graph (source ${BUNDLE_SHA:0:12}, graphify ${BUNDLE_GRAPHIFY_VERSION}) into ${GRAPH_DIR}/."
exit 0
