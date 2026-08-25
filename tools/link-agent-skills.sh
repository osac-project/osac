#!/usr/bin/env bash
# Consumer wrapper: materialize OSAC skills from a vendored osac-ai-skills
# clone, then exec that repo's fan-out with PROJECT_ROOT set to this repo.
#
# Usage: tools/link-agent-skills.sh [--claude] [--cursor] [--gemini] [--all]
#          [--with-ai-workflows] [--verify]
#
# Vendor resolution:
#   $OSAC_AI_SKILLS_VENDOR_DIR, if set (tools/bootstrap.sh sets this to the
#     vendor it already resolved/cloned, so this wrapper can't independently
#     pick a different, possibly stale, directory)
#   otherwise, first match: ~/.osac-ai-skills | $REPO/.osac-ai-skills
#
# Default flags when none given: --all --with-ai-workflows
set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
# REPO_ROOT: this wrapper's own name for its repo root (osac-workspace's
# reference wrapper calls the same concept WORKSPACE_ROOT). Exported to the
# vendored fan-out below as PROJECT_ROOT instead — that name is a fixed
# cross-repo contract (see osac-ai-skills' fan-out), not a stylistic
# choice, so it isn't renamed to match.
REPO_ROOT="$(realpath "${SCRIPT_DIR}/..")"

usage() {
  cat <<EOF
Usage: $(basename "$0") [--claude] [--cursor] [--gemini] [--all] [--with-ai-workflows] [--verify]

  Consumer wrapper for osac-ai-skills fan-out. Materializes skills/ symlinks
  from the vendored clone, then runs that clone's tools/link-agent-skills.sh
  with PROJECT_ROOT=${REPO_ROOT}.

  --claude / --cursor / --gemini / --all / --with-ai-workflows / --verify
      Passed through to the vendored fan-out (see osac-ai-skills README).
EOF
}

# Content-based check only (no .git requirement), unlike bootstrap.sh's
# osac_ai_skills_vendor_ok(). This wrapper never runs git against the vendor
# dir — it only reads files — so it doesn't share bootstrap.sh's need for a
# real git checkout. Staying content-based also matches the reference
# osac-workspace wrapper and the upstream osac-ai-skills fan-out itself
# (neither checks .git), and leaves room for the non-git vendoring mechanisms
# ADR 0001 Decision item 3 still has open (git subtree / copy-bot) for
# automated-framework consumption.
#
# When $OSAC_AI_SKILLS_VENDOR_DIR is set, it is authoritative and is not
# re-validated against the two-candidate search below: bootstrap.sh sets it
# to the exact directory it already resolved (or just cloned), specifically
# to prevent this function from re-resolving independently and picking a
# different candidate — e.g. a content-valid-but-non-git ~/.osac-ai-skills
# that bootstrap.sh already rejected in favor of a fresh project-local clone.
resolve_osac_ai_skills_dir() {
  local dir
  if [[ -n "${OSAC_AI_SKILLS_VENDOR_DIR:-}" ]]; then
    if [[ -d "${OSAC_AI_SKILLS_VENDOR_DIR}/skills" && -x "${OSAC_AI_SKILLS_VENDOR_DIR}/tools/link-agent-skills.sh" ]]; then
      (cd "${OSAC_AI_SKILLS_VENDOR_DIR}" && pwd -P)
      return 0
    fi
    return 1
  fi
  for dir in "${HOME}/.osac-ai-skills" "${REPO_ROOT}/.osac-ai-skills"; do
    if [[ -d "${dir}/skills" && -x "${dir}/tools/link-agent-skills.sh" ]]; then
      (cd "${dir}" && pwd -P)
      return 0
    fi
  done
  return 1
}

# Absolute symlink skills/<name> -> <vendor>/skills/<name>.
# Refuses to replace a real (non-symlink) path.
# Prunes stale symlinks that still point into either candidate vendor root
# (not just the currently-active one) after removals — resolution can flip
# between ~/.osac-ai-skills and ${REPO_ROOT}/.osac-ai-skills across runs, and
# a symlink materialized from the previously-active vendor must still be
# recognized as vendor-managed even after the active vendor changes.
materialize_osac_skills() {
  local vendor="$1"
  local skill_dir name link_path target vendor_skills existing link_target
  local candidate managed_root known_roots=()

  vendor_skills="$(cd "${vendor}/skills" && pwd -P)"
  for candidate in "${HOME}/.osac-ai-skills/skills" "${REPO_ROOT}/.osac-ai-skills/skills"; do
    known_roots+=("${candidate}")
    [[ -d "${candidate}" ]] && known_roots+=("$(cd "${candidate}" && pwd -P)")
  done

  mkdir -p "${REPO_ROOT}/skills"
  for skill_dir in "${vendor}/skills"/*/; do
    [[ -d "${skill_dir}" ]] || continue
    name="$(basename "${skill_dir}")"
    case "${name}" in
      bugfix|design|e2e|implement|prd|_shared) continue ;;
    esac
    link_path="${REPO_ROOT}/skills/${name}"
    target="$(cd "${skill_dir}" && pwd -P)"
    if [[ -L "${link_path}" ]]; then
      rm -f "${link_path}"
    elif [[ -e "${link_path}" ]]; then
      echo "ERROR: ${link_path} exists and is not a symlink; refusing to replace (remove or rename the real directory, then re-run)" >&2
      return 1
    fi
    ln -sfn "${target}" "${link_path}"
  done

  for existing in "${REPO_ROOT}/skills"/*; do
    [[ -e "${existing}" || -L "${existing}" ]] || continue
    [[ -L "${existing}" ]] || continue
    name="$(basename "${existing}")"
    case "${name}" in
      bugfix|design|e2e|implement|prd|_shared) continue ;;
    esac
    link_target="$(readlink "${existing}")"
    managed_root=false
    for candidate in "${known_roots[@]}"; do
      case "${link_target}" in
        "${candidate}"/*) managed_root=true; break ;;
      esac
    done
    [[ "${managed_root}" == true ]] || continue
    if [[ ! -d "${vendor_skills}/${name}" ]]; then
      rm -f "${existing}"
    fi
  done
}

# origin/main tools/bootstrap.sh ran ai-workflows install.sh first, which
# mkdir -p's .claude/skills (and .cursor/.gemini) as real directories of
# generated workflow links. Fan-out safe_symlink refuses to replace a real
# directory, so a contributor who already bootstrapped on main would fail
# on the first refresh after this PR. Those trees are gitignored generated
# output; drop them once so umbrellas can become ../skills symlinks.
# Existing umbrellas that are already symlinks are left alone.
#
# Agent selection matches the vendored osac-ai-skills fan-out (the same
# parser osac-workspace execs): only clear agents this invocation will
# actually link. --verify with no --claude/--cursor/--gemini/--all is
# read-only and must not delete anything.
clear_legacy_real_umbrella_skill_dirs() {
  local link_claude=false link_cursor=false link_gemini=false verify_only=false
  local arg agent_dir skills_path
  local -a dirs=()

  for arg in "${ARGS[@]}"; do
    case "${arg}" in
      --claude) link_claude=true ;;
      --cursor) link_cursor=true ;;
      --gemini) link_gemini=true ;;
      --all)
        link_claude=true
        link_cursor=true
        link_gemini=true
        ;;
      --verify) verify_only=true ;;
    esac
  done

  if [[ "${verify_only}" == true \
     && "${link_claude}" == false \
     && "${link_cursor}" == false \
     && "${link_gemini}" == false ]]; then
    return 0
  fi

  [[ "${link_claude}" == true ]] && dirs+=("${REPO_ROOT}/.claude")
  [[ "${link_cursor}" == true ]] && dirs+=("${REPO_ROOT}/.cursor")
  [[ "${link_gemini}" == true ]] && dirs+=("${REPO_ROOT}/.gemini")

  for agent_dir in "${dirs[@]}"; do
    skills_path="${agent_dir}/skills"
    if [[ -e "${skills_path}" && ! -L "${skills_path}" ]]; then
      echo "Removing real ${skills_path} (legacy bootstrap leftover) so it can become a symlink umbrella" >&2
      rm -rf "${skills_path}"
    fi
  done
}

ARGS=("$@")
if [[ ${#ARGS[@]} -eq 0 ]]; then
  ARGS=(--all --with-ai-workflows)
fi

for arg in "${ARGS[@]}"; do
  case "${arg}" in
    -h|--help)
      usage
      exit 0
      ;;
  esac
done

VENDOR_DIR="$(resolve_osac_ai_skills_dir)" || {
  if [[ -n "${OSAC_AI_SKILLS_VENDOR_DIR:-}" ]]; then
    echo "ERROR: OSAC_AI_SKILLS_VENDOR_DIR=${OSAC_AI_SKILLS_VENDOR_DIR} is not a usable osac-ai-skills vendor (missing skills/ or tools/link-agent-skills.sh)." >&2
  else
    echo "ERROR: osac-ai-skills vendor not found." >&2
    echo "Expected ~/.osac-ai-skills or ${REPO_ROOT}/.osac-ai-skills with skills/ and tools/link-agent-skills.sh." >&2
    echo "Run tools/bootstrap.sh (or clone osac-project/osac-ai-skills into .osac-ai-skills)." >&2
  fi
  exit 1
}

materialize_osac_skills "${VENDOR_DIR}"
clear_legacy_real_umbrella_skill_dirs

export PROJECT_ROOT="${REPO_ROOT}"
exec "${VENDOR_DIR}/tools/link-agent-skills.sh" "${ARGS[@]}"
