#!/usr/bin/env bash
# Install AI workflow skills for this repo.
#
# Usage: tools/bootstrap.sh [--no-fork] [--fork-name NAME]
#
# This script:
#   1. Clones or updates osac-project/osac-ai-skills (prefers ~/.osac-ai-skills)
#   2. Clones or updates flightctl/ai-workflows (prefers ~/.ai-workflows)
#   3. Materializes skills/ and links Claude/Cursor/Gemini discovery dirs
#      (umbrella symlinks must exist before ai-workflows install.sh, which
#      writes workflow links into those paths)
#   4. Installs workflows (bugfix, implement, prd, design, e2e)
#   5. Clones skill-relative sibling repos under this checkout
#      (enhancement-proposals, osac-ux, osac-ui, osac-docs). osac-docs
#      is osac-project/docs — not docs/. E2E suites live in-tree at
#      tests/e2e/; osac-test-infra is not cloned.
#      Writeable siblings get a push remote (default name `fork`;
#      origin = osac-project). --fork-name only rearranges remotes on
#      those siblings — not this checkout, osac-ux, or vendor clones.
#
# Re-run anytime to update to latest main.
set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
PROJECT_ROOT="$(realpath "${SCRIPT_DIR}/..")"

GITHUB_ORG="osac-project"
NO_FORK=false
FORK_REMOTE_NAME="fork"
GH_USER=""
GIT_PROTOCOL="https"

usage() {
  cat <<'EOF'
Usage: tools/bootstrap.sh [--no-fork] [--fork-name NAME]

Vendors AI skills/workflows and clones skill-relative sibling repos under
this checkout.

By default, each writeable sibling is forked to your GitHub account:
  origin     = osac-project/<repo>     (upstream source, PR target)
  <fork-name> = <your-username>/<repo>  (push target; default name: fork)

--fork-name origin uses the conventional GitHub layout on writeable
siblings only (origin = your fork, upstream = osac-project). Pick a name
and stick with it — re-running with a different name mutates remotes.
This checkout, osac-ux, and vendor clones are never renamed. Skills
resolve remotes by URL, not by name. The GitHub fork of osac-project/docs
is named osac-docs (override extra mappings in tools/fork-overrides.sh).

osac-ux is a reference clone (no fork). Vendor checkouts (.osac-ai-skills,
.ai-workflows) are never forked. --no-fork skips forking even when
--fork-name is also passed. After --fork-name origin, a later --no-fork
run does not rewrite remotes and skips updates on those origin-as-fork
siblings (no gh).

Options:
  --no-fork          Clone siblings from osac-project without forking.
                     Useful for read-only access or CI environments.
                     Does not update siblings already using --fork-name origin.
  --fork-name NAME   Name for the writeable-sibling push remote (default: fork).
  --help             Show this help message.

Prerequisites:
  - gh CLI installed and authenticated (gh auth login), unless --no-fork
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-fork) NO_FORK=true; shift ;;
    --fork-name)
      [[ -n "${2:-}" && "$2" != --* ]] || { echo "Error: --fork-name requires a value" >&2; usage >&2; exit 1; }
      FORK_REMOTE_NAME="$2"
      shift 2
      ;;
    --help|-h) usage; exit 0 ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

# Nested osac-workspace/osac would install a second skill overlay (two
# PROJECT_ROOTs). This repo is the project root — abort that one path so
# people use a standalone clone or worktree. Temporary until osac-workspace
# is decommissioned. Override: OSAC_ALLOW_NESTED_BOOTSTRAP=1
if [[ "${OSAC_ALLOW_NESTED_BOOTSTRAP:-}" != "1" ]] \
   && [[ -x "${PROJECT_ROOT}/../bootstrap.sh" ]]; then
  nested_osac="$(realpath "${PROJECT_ROOT}/../osac" 2>/dev/null || true)"
  if [[ -n "${nested_osac}" && "${nested_osac}" == "${PROJECT_ROOT}" ]]; then
    echo "ERROR: this osac/ checkout is inside osac-workspace." >&2
    echo "This repo is the project root — use a standalone clone or worktree," >&2
    echo "then run tools/bootstrap.sh there (not from osac-workspace/osac)." >&2
    echo "To force this script anyway: OSAC_ALLOW_NESTED_BOOTSTRAP=1 $0" >&2
    exit 1
  fi
fi

if [[ "$NO_FORK" == false ]]; then
  if ! command -v gh &>/dev/null; then
    echo "ERROR: gh CLI is not installed." >&2
    echo "Install it (https://cli.github.com/) or use --no-fork for read-only clone." >&2
    exit 1
  fi
  if ! gh auth status &>/dev/null; then
    echo "ERROR: gh CLI is not authenticated." >&2
    echo "Run 'gh auth login' or use --no-fork for read-only clone." >&2
    exit 1
  fi
  GH_USER=$(gh api user -q .login)
  if [[ -z "$GH_USER" || "$GH_USER" == "null" ]]; then
    echo "ERROR: gh API returned an empty GitHub username." >&2
    echo "Cannot construct fork URLs. Run 'gh auth login' or use --no-fork for read-only clone." >&2
    exit 1
  fi
  GIT_PROTOCOL=$(gh config get git_protocol 2>/dev/null || echo "https")
  echo "Bootstrapping OSAC for GitHub user: ${GH_USER}"
else
  echo "Bootstrapping OSAC (read-only, no forks)..."
fi

OSAC_AI_SKILLS_REPO="osac-project/osac-ai-skills"
HOME_OSAC_AI_SKILLS="${HOME}/.osac-ai-skills"
REPO_OSAC_AI_SKILLS="${PROJECT_ROOT}/.osac-ai-skills"
HOME_AI_WORKFLOWS="${HOME}/.ai-workflows"
REPO_AI_WORKFLOWS="${PROJECT_ROOT}/.ai-workflows"

# Linked worktrees store .git as a file; require a work-tree root so a
# leftover directory inside some other checkout does not inherit that parent
# (git -C would otherwise fetch/rebase the enclosing repo).
is_git_work_tree_root() {
  local dir="$1"
  [[ -n "$dir" && -d "$dir" ]] \
    && git -C "$dir" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    && [[ -z "$(git -C "$dir" rev-parse --show-prefix 2>/dev/null)" ]]
}

# Git-capable check — gates the git fetch/rebase below. tools/link-agent-skills.sh's
# resolve_osac_ai_skills_dir() intentionally uses a weaker, content-based check
# instead (skills/ + executable fan-out, no .git): it never runs git against the
# vendor dir, and staying content-based matches the reference osac-workspace
# wrapper and the upstream osac-ai-skills fan-out itself (neither checks .git),
# while leaving room for non-git vendoring mechanisms ADR 0001 Decision item 3
# still has open (git subtree / copy-bot) for automated-framework consumption.
osac_ai_skills_vendor_ok() {
  local dir="$1"
  is_git_work_tree_root "$dir" \
    && [[ -d "${dir}/skills" ]] \
    && [[ -x "${dir}/tools/link-agent-skills.sh" ]]
}

ai_workflows_checkout_ok() {
  local dir="$1"
  is_git_work_tree_root "$dir" && [[ -x "${dir}/install.sh" ]]
}

# Fetch + rebase a git checkout onto $remote/main (default origin). Warns and
# continues on failure rather than exiting — a stale checkout is recoverable
# manually. Skip when HEAD is not main so a later bootstrap does not rebase a
# feature branch (siblings now have a push remote; developers will check those
# out). Vendors always pass origin (or omit the third arg).
update_git_repo() {
  local dir="$1" label="$2" remote="${3:-origin}"
  local branch
  branch=$(git -C "$dir" symbolic-ref --short HEAD 2>/dev/null || echo "")
  if [[ "$branch" != "main" ]]; then
    echo "  ${label} is on '${branch:-unknown}'. Skipping update to avoid rebasing your work."
    return 0
  fi
  if ! (cd "${dir}" && git fetch "$remote" -q); then
    echo "  Fetch failed for ${label}. Skipping update."
  elif ! (cd "${dir}" && git rebase "${remote}/main" --autostash -q); then
    (cd "${dir}" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed for ${label}. Resolve manually: cd ${dir} && git rebase ${remote}/main"
  else
    echo "  ${label} up to date"
  fi
}

# --- osac-ai-skills ---

if [[ -d "${HOME_OSAC_AI_SKILLS}" ]] && osac_ai_skills_vendor_ok "${HOME_OSAC_AI_SKILLS}"; then
  OSAC_AI_SKILLS_DIR="$(readlink -f "${HOME_OSAC_AI_SKILLS}")"
  echo "Updating osac-ai-skills (${OSAC_AI_SKILLS_DIR})..."
  update_git_repo "${OSAC_AI_SKILLS_DIR}" "osac-ai-skills"
elif [[ -d "${REPO_OSAC_AI_SKILLS}" ]] && osac_ai_skills_vendor_ok "${REPO_OSAC_AI_SKILLS}"; then
  OSAC_AI_SKILLS_DIR="${REPO_OSAC_AI_SKILLS}"
  echo "Updating osac-ai-skills (.osac-ai-skills)..."
  update_git_repo "${OSAC_AI_SKILLS_DIR}" "osac-ai-skills"
elif [[ -d "${REPO_OSAC_AI_SKILLS}" ]]; then
  echo "ERROR: ${REPO_OSAC_AI_SKILLS} exists but is not a usable vendor checkout." >&2
  echo "Expected a git clone with skills/ and an executable tools/link-agent-skills.sh." >&2
  echo "Remove or rename that directory, then re-run tools/bootstrap.sh to clone a fresh copy." >&2
  exit 1
else
  if [[ -d "${HOME_OSAC_AI_SKILLS}" ]]; then
    echo "  ${HOME_OSAC_AI_SKILLS} exists but is not a usable vendor checkout; using ${REPO_OSAC_AI_SKILLS}"
  fi
  OSAC_AI_SKILLS_DIR="${REPO_OSAC_AI_SKILLS}"
  echo "Cloning osac-ai-skills..."
  git clone "https://github.com/${OSAC_AI_SKILLS_REPO}.git" "${OSAC_AI_SKILLS_DIR}"
fi

AI_WORKFLOWS_REPO="flightctl/ai-workflows"
AI_WORKFLOWS_DIR=""

# --- ai-workflows (flightctl) ---

if ai_workflows_checkout_ok "${HOME_AI_WORKFLOWS}"; then
  AI_WORKFLOWS_DIR="$(readlink -f "${HOME_AI_WORKFLOWS}")"
  echo "Updating ai-workflows (${AI_WORKFLOWS_DIR})..."
  update_git_repo "${AI_WORKFLOWS_DIR}" "ai-workflows"
elif ai_workflows_checkout_ok "${REPO_AI_WORKFLOWS}"; then
  AI_WORKFLOWS_DIR="${REPO_AI_WORKFLOWS}"
  echo "Updating ai-workflows (.ai-workflows)..."
  update_git_repo "${AI_WORKFLOWS_DIR}" "ai-workflows"
elif [[ -d "${REPO_AI_WORKFLOWS}" ]]; then
  echo "ERROR: ${REPO_AI_WORKFLOWS} exists but is not a usable ai-workflows checkout." >&2
  echo "Expected a git clone with an executable install.sh." >&2
  echo "Remove or rename that directory, then re-run tools/bootstrap.sh to clone a fresh copy." >&2
  exit 1
else
  if [[ -d "${HOME_AI_WORKFLOWS}" ]]; then
    echo "  ${HOME_AI_WORKFLOWS} exists but is not a usable ai-workflows checkout; using ${REPO_AI_WORKFLOWS}"
  fi
  AI_WORKFLOWS_DIR="${REPO_AI_WORKFLOWS}"
  echo "Cloning ai-workflows..."
  git clone "https://github.com/${AI_WORKFLOWS_REPO}.git" "${AI_WORKFLOWS_DIR}"
fi

# Umbrella .*/skills -> ../skills must exist before install.sh, which mkdir -p's
# those paths and writes workflow symlinks into them (through the umbrellas).
#
# Export the vendor dir this script just resolved/updated/cloned above so the
# wrapper uses that exact directory instead of independently re-resolving one
# — the wrapper's resolve_osac_ai_skills_dir() uses a weaker, content-only
# check (see its own comment) and could otherwise pick a different, possibly
# stale, ~/.osac-ai-skills that this script already rejected for git updates.
echo "Linking agent skill directories..."
OSAC_AI_SKILLS_VENDOR_DIR="${OSAC_AI_SKILLS_DIR}" "${PROJECT_ROOT}/tools/link-agent-skills.sh"

echo "Installing ai-workflows skills..."
AI_WORKFLOWS="bugfix,implement,prd,design,e2e"
"${AI_WORKFLOWS_DIR}/install.sh" all --project "${PROJECT_ROOT}" --workflows "${AI_WORKFLOWS}"

# Optional tools/fork-overrides.sh may replace or append FORK_OVERRIDE_PAIRS
# entries ("upstream-repo:github-fork-name"). docs defaults to osac-docs.
FORK_OVERRIDE_PAIRS=()
if [[ -f "${SCRIPT_DIR}/fork-overrides.sh" ]]; then
  # shellcheck disable=SC1091
  source "${SCRIPT_DIR}/fork-overrides.sh"
fi
_fork_override_has_docs=false
for _pair in "${FORK_OVERRIDE_PAIRS[@]+"${FORK_OVERRIDE_PAIRS[@]}"}"; do
  if [[ "${_pair%%:*}" == "docs" ]]; then
    _fork_override_has_docs=true
    break
  fi
done
if [[ "$_fork_override_has_docs" == false ]]; then
  FORK_OVERRIDE_PAIRS+=("docs:osac-docs")
fi
unset _fork_override_has_docs _pair

fork_repo_for() {
  local repo="$1" pair
  for pair in "${FORK_OVERRIDE_PAIRS[@]+"${FORK_OVERRIDE_PAIRS[@]}"}"; do
    if [[ "${pair%%:*}" == "$repo" ]]; then
      echo "${pair#*:}"
      return 0
    fi
  done
  echo "$repo"
}

# Skill-relative sibling checkouts (gitignored). Local dir name follows the
# GitHub repo except docs → osac-docs (docs/ is in-tree conventions).
# Format: "repo" or "repo:local-dir"
SIBLINGS=(
  "enhancement-proposals"
  "osac-ux"
  "osac-ui"
  "docs:osac-docs"
)

# True when $url is a path or SSH remote for $suffix (e.g. osac-project/docs).
# Require / or : before the suffix so evil-osac-project/<repo> does not match.
remote_url_matches_suffix() {
  local url="$1" suffix="$2"
  local stripped="${url%.git}"
  [[ "$stripped" == *"/${suffix}" || "$stripped" == *":${suffix}" ]]
}

find_upstream_remote() {
  local dir="$1" repo="$2"
  local remote url
  local expected_suffix="${GITHUB_ORG}/${repo}"
  for remote in $(git -C "$dir" remote 2>/dev/null); do
    url=$(git -C "$dir" remote get-url "$remote" 2>/dev/null) || continue
    if remote_url_matches_suffix "$url" "$expected_suffix"; then
      echo "$remote"
      return 0
    fi
  done
  return 1
}

is_expected_sibling() {
  local dir="$1" repo="$2"
  local url
  url=$(git -C "$dir" remote get-url origin 2>/dev/null) || return 1
  if remote_url_matches_suffix "$url" "${GITHUB_ORG}/${repo}"; then
    return 0
  fi
  # --fork-name origin layout: origin is the user's fork, another remote is org.
  if [[ "$NO_FORK" == false && -n "$GH_USER" ]] \
     && remote_url_matches_suffix "$url" "${GH_USER}/$(fork_repo_for "$repo")"; then
    find_upstream_remote "$dir" "$repo" >/dev/null
    return $?
  fi
  return 1
}

sibling_update_remote() {
  local dir="$1" repo="$2"
  local url
  url=$(git -C "$dir" remote get-url origin 2>/dev/null) || { echo origin; return 0; }
  if remote_url_matches_suffix "$url" "${GITHUB_ORG}/${repo}"; then
    echo origin
    return 0
  fi
  find_upstream_remote "$dir" "$repo"
}

get_fork_url() {
  local repo="$1"
  local fork_repo
  fork_repo=$(fork_repo_for "$repo")
  if [[ "$GIT_PROTOCOL" == "ssh" ]]; then
    echo "git@github.com:${GH_USER}/${fork_repo}.git"
  else
    echo "https://github.com/${GH_USER}/${fork_repo}.git"
  fi
}

# Returns 0 if every push URL for $remote in $dir is a path/SSH match for
# $expected_suffix (e.g. $GH_USER/$repo). Require / or : before the suffix
# so user "ed" does not match github.com/fred/<repo>. Checks push URLs.
fork_remote_push_matches() {
  local dir="$1" remote="$2" expected_suffix="$3"
  local push_urls
  push_urls=$(git -C "$dir" remote get-url --push --all "$remote" 2>/dev/null) || return 1
  [[ -n "$push_urls" ]] || return 1
  local push_url
  while IFS= read -r push_url; do
    remote_url_matches_suffix "$push_url" "$expected_suffix" || return 1
  done <<< "$push_urls"
  return 0
}

# True when $remote already *is* the user fork: fetch URL and every push URL
# match $expected_suffix. Push-only (org fetch + fork pushurl) is not enough.
fork_remote_is_user_fork() {
  local dir="$1" remote="$2" expected_suffix="$3"
  local fetch_url
  fetch_url=$(git -C "$dir" remote get-url "$remote" 2>/dev/null) || return 1
  remote_url_matches_suffix "$fetch_url" "$expected_suffix" || return 1
  fork_remote_push_matches "$dir" "$remote" "$expected_suffix"
}

# git remote rename retargets branch.*.remote to the new name. After we add
# the fork back as $to_remote, point those branches at the fork again so
# `git push` on main does not go to osac-project.
restore_branch_remotes() {
  local dir="$1" from_remote="$2" to_remote="$3"
  local line key value branch configs
  configs=$(git -C "$dir" config --get-regexp '^branch\..*\.remote$' 2>/dev/null || true)
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    key="${line%% *}"
    value="${line#* }"
    [[ "$value" == "$from_remote" ]] || continue
    branch="${key#branch.}"
    branch="${branch%.remote}"
    git -C "$dir" config "branch.${branch}.remote" "$to_remote"
  done <<< "$configs"
}

# True when $GH_USER/$(fork_repo_for "$repo") is a GitHub fork of
# osac-project/$repo (not an unrelated same-name repo — common for docs,
# whose fork is osac-docs).
is_github_fork_of_org_repo() {
  local repo="$1"
  local parent fork_repo
  fork_repo=$(fork_repo_for "$repo")
  parent=$(gh repo view "${GH_USER}/${fork_repo}" --json parent -q '.parent.nameWithOwner // empty' 2>/dev/null) || return 1
  [[ "$parent" == "${GITHUB_ORG}/${repo}" ]]
}

ensure_fork_remote() {
  local repo="$1" dir="$2"
  local url occupant target renamed_to="" fork_repo
  local fork_name_args=()
  fork_repo=$(fork_repo_for "$repo")
  if [[ "$fork_repo" != "$repo" ]]; then
    fork_name_args=(--fork-name "$fork_repo")
  fi
  if ! gh repo fork "${GITHUB_ORG}/${repo}" --clone=false --default-branch-only \
       ${fork_name_args[@]+"${fork_name_args[@]}"}; then
    if ! is_github_fork_of_org_repo "$repo"; then
      echo "  Failed to fork ${GITHUB_ORG}/${repo}. Skipping fork remote."
      return 1
    fi
  fi
  url=$(get_fork_url "$repo")
  if git -C "$dir" remote get-url "$FORK_REMOTE_NAME" &>/dev/null; then
    occupant=$(git -C "$dir" remote get-url "$FORK_REMOTE_NAME")
    if fork_remote_is_user_fork "$dir" "$FORK_REMOTE_NAME" "${GH_USER}/${fork_repo}"; then
      return 0
    fi
    if remote_url_matches_suffix "$occupant" "${GITHUB_ORG}/${repo}"; then
      target="upstream"
      while git -C "$dir" remote get-url "$target" &>/dev/null; do
        target="osac-${target}"
      done
      git -C "$dir" remote rename "$FORK_REMOTE_NAME" "$target"
      renamed_to="$target"
      # Rename keeps a leftover pushurl (org fetch + fork push). Drop it so
      # the org remote fetches and pushes to osac-project.
      git -C "$dir" config --unset-all "remote.${target}.pushurl" 2>/dev/null || true
      echo "  Renamed existing '${FORK_REMOTE_NAME}' → '${target}'"
    else
      echo "  Remote '${FORK_REMOTE_NAME}' already exists with a different URL. Skipping."
      return 1
    fi
  fi
  if ! git -C "$dir" remote add "$FORK_REMOTE_NAME" "$url"; then
    if [[ -n "$renamed_to" ]]; then
      git -C "$dir" remote rename "$renamed_to" "$FORK_REMOTE_NAME" 2>/dev/null || true
    fi
    echo "  Failed to add '${FORK_REMOTE_NAME}' remote for ${repo}."
    return 1
  fi
  if [[ -n "$renamed_to" ]]; then
    restore_branch_remotes "$dir" "$renamed_to" "$FORK_REMOTE_NAME"
  fi
  git -C "$dir" fetch "$FORK_REMOTE_NAME" || {
    echo "  Fetch of ${FORK_REMOTE_NAME} failed for ${repo}. Remote was added."
    return 0
  }
}

# Writeable siblings get a fork remote. osac-ux is reference-only; vendors
# never enter this loop.
should_fork_sibling() {
  local repo="$1"
  [[ "$NO_FORK" == false ]] || return 1
  [[ "$repo" != "osac-ux" ]] || return 1
  return 0
}

maybe_fork_sibling() {
  local repo="$1" dest="$2"
  should_fork_sibling "$repo" || return 0
  if fork_remote_is_user_fork "$dest" "$FORK_REMOTE_NAME" "${GH_USER}/$(fork_repo_for "$repo")"; then
    return 0
  fi
  echo "Adding ${FORK_REMOTE_NAME} remote for ${repo}..."
  ensure_fork_remote "$repo" "$dest" || echo "  Fork remote for ${repo} failed. Continuing."
}

ensure_sibling() {
  local repo="$1" dir="$2"
  local dest="${PROJECT_ROOT}/${dir}"
  dest="${dest%/}"
  # Single child of PROJECT_ROOT — no empty, ., .., or slash (blocks ../victim).
  if [[ -z "$dir" || "$dir" == "." || "$dir" == ".." || "$dir" == */* || "$dest" == "$PROJECT_ROOT" ]]; then
    echo "Skipping ${repo} — invalid sibling directory '${dir}'."
    return 0
  fi
  if [[ -d "${dest}" ]] && is_expected_sibling "${dest}" "${repo}"; then
    echo "Updating ${dir}..."
    local update_remote
    update_remote=$(sibling_update_remote "${dest}" "${repo}") || update_remote=origin
    update_git_repo "${dest}" "${dir}" "$update_remote"
    maybe_fork_sibling "$repo" "$dest"
  elif [[ -d "${dest}" ]]; then
    echo "Skipping ${dir} — directory exists but is not a clone of ${GITHUB_ORG}/${repo}."
  else
    echo "Cloning ${repo} into ${dir}..."
    if ! git clone "https://github.com/${GITHUB_ORG}/${repo}.git" "${dest}"; then
      echo "  Clone failed for ${repo}. Skipping."
      if [[ "$dest" == "$PROJECT_ROOT"/* ]]; then
        rm -rf "${dest}" 2>/dev/null || true
      fi
      return 0
    fi
    maybe_fork_sibling "$repo" "$dest"
  fi
}

echo "Ensuring sibling repos..."
for entry in "${SIBLINGS[@]}"; do
  repo="${entry%%:*}"
  dir="${entry#*:}"
  ensure_sibling "${repo}" "${dir}"
done

echo ""
echo "AI workflows and OSAC skills installed."
