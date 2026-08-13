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

# --- Step 2: pull the latest published bundle (public repo, no auth needed) ---
if ! gh release download "${RELEASE_TAG}" --repo "${REPO}" \
      --pattern 'graphify-bundle.tar.gz' --dir "${TMP_DIR}" >/dev/null 2>&1; then
  fall_back_to_local "Could not fetch latest graph bundle (network/404/rate-limit)"
fi

mkdir -p "${TMP_DIR}/extracted"
if ! tar xzf "${TMP_DIR}/graphify-bundle.tar.gz" -C "${TMP_DIR}/extracted" 2>/dev/null; then
  fall_back_to_local "Downloaded bundle is corrupt (failed to extract)"
fi

# --- Step 3: validate graph.json AND metadata.json before touching local state ---
GRAPH_JSON="${TMP_DIR}/extracted/graph.json"
META_JSON="${TMP_DIR}/extracted/metadata.json"

if [[ ! -f "${GRAPH_JSON}" ]] || ! jq -e 'has("nodes") and has("edges")' "${GRAPH_JSON}" >/dev/null 2>&1; then
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
    : # bundle's source is an ancestor of HEAD -- normal, no staleness note needed.
  else
    warn "Note: published graph's source (${BUNDLE_SHA:0:12}) is not an ancestor of local HEAD -- local checkout may be on a branch ahead of or diverged from what generated this graph."
  fi
else
  warn "Note: could not compare graph source SHA against local HEAD (shallow clone or unrelated history) -- staleness unknown, proceeding on TTL basis only."
fi

# --- Step 5: version check ---
LOCAL_GRAPHIFY_VERSION="$(graphify --version 2>/dev/null | tr -d '[:space:]')"
if [[ -n "${LOCAL_GRAPHIFY_VERSION}" && "${LOCAL_GRAPHIFY_VERSION}" != "${BUNDLE_GRAPHIFY_VERSION}" ]]; then
  warn "graphify version mismatch: published graph was generated with ${BUNDLE_GRAPHIFY_VERSION}, local install is ${LOCAL_GRAPHIFY_VERSION}. Refusing to load -- upgrade with: uv tool install --upgrade graphifyy (or: pipx upgrade graphifyy)."
  fall_back_to_local "Version mismatch"
fi

# --- Step 6: atomic swap ---
date -u +%s > "${TMP_DIR}/extracted/.last-fetch-at"
rm -rf "${GRAPH_DIR}.tmp"
mv "${TMP_DIR}/extracted" "${GRAPH_DIR}.tmp"
rm -rf "${GRAPH_DIR}"
mv "${GRAPH_DIR}.tmp" "${GRAPH_DIR}"

warn "Fetched graph (source ${BUNDLE_SHA:0:12}, graphify ${BUNDLE_GRAPHIFY_VERSION}) into ${GRAPH_DIR}/."
exit 0
