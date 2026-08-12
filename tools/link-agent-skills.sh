#!/usr/bin/env bash
# Consumer wrapper: materialize OSAC skills from a vendored osac-ai-skills
# clone, then exec that repo's fan-out with PROJECT_ROOT set to this repo.
#
# Usage: tools/link-agent-skills.sh [--claude] [--cursor] [--gemini] [--all]
#          [--with-ai-workflows] [--verify]
#
# Vendor resolution (first match):
#   ~/.osac-ai-skills
#   $REPO/.osac-ai-skills
#
# Default flags when none given: --all --with-ai-workflows
set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
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

resolve_osac_ai_skills_dir() {
  local dir
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
  echo "ERROR: osac-ai-skills vendor not found." >&2
  echo "Expected ~/.osac-ai-skills or ${REPO_ROOT}/.osac-ai-skills with skills/ and tools/link-agent-skills.sh." >&2
  echo "Run tools/bootstrap.sh (or clone osac-project/osac-ai-skills into .osac-ai-skills)." >&2
  exit 1
}

materialize_osac_skills "${VENDOR_DIR}"

export PROJECT_ROOT="${REPO_ROOT}"
exec "${VENDOR_DIR}/tools/link-agent-skills.sh" "${ARGS[@]}"
