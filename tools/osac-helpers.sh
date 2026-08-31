#!/bin/bash
# shellcheck shell=bash
#
# osac-helpers.sh — source this file to get OSAC developer workflow utilities.
#
# Usage:
#   source tools/osac-helpers.sh
#   osac-new-worktree feat/my-feature
#
# Safe to source from bash or zsh (macOS default). Do not use BASH_REMATCH.

# First OSAC-NNNN in a branch name; empty if none. POSIX so zsh nounset is fine.
osac_jira_ticket_from_branch() {
    printf '%s\n' "${1:-}" | sed -n 's/.*\(OSAC-[0-9][0-9]*\).*/\1/p'
}

osac-new-worktree() {
    if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        echo "Error: not inside a Git repository." >&2
        return 1
    fi

    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || return 1
    if [[ ! -x "${repo_root}/tools/bootstrap.sh" ]]; then
        echo "Error: this command must be run from an osac clone (missing tools/bootstrap.sh)." >&2
        echo "  Current repository root: ${repo_root}" >&2
        return 1
    fi

    if [[ -z "${1:-}" ]]; then
        echo "Error: please provide a branch name." >&2
        echo "Usage: osac-new-worktree <branch-name>" >&2
        return 1
    fi

    local branch_name=$1
    if [[ "$branch_name" =~ [[:space:]] || "$branch_name" == *..* || "$branch_name" == /* ]]; then
        echo "Error: branch name must not contain spaces, '..', or start with '/'." >&2
        return 1
    fi

    local worktree_suffix target_dir worktree_parent
    worktree_suffix=$(basename "$branch_name")
    worktree_parent="${OSAC_WORKTREE_PARENT:-$(dirname "$repo_root")}"
    target_dir="${worktree_parent}/osac-${worktree_suffix}"

    echo "Creating worktree for branch '${branch_name}' at '${target_dir}'..."
    if ! git -C "$repo_root" worktree add -b "$branch_name" "$target_dir"; then
        echo "Error: failed to create worktree." >&2
        echo "  Possible causes:" >&2
        echo "  - Branch '${branch_name}' already exists (use: git branch -d ${branch_name})" >&2
        echo "  - Worktree path conflicts with an existing directory" >&2
        echo "  To list existing worktrees: git worktree list" >&2
        return 1
    fi

    cd "$target_dir" || return 1
    echo "Switched to worktree: $(pwd)"

    if ! ./tools/bootstrap.sh; then
        echo "Error: tools/bootstrap.sh failed." >&2
        return 1
    fi

    local ticket=""
    ticket=$(osac_jira_ticket_from_branch "$branch_name")
    if [[ -n "$ticket" ]]; then
        echo "Fetching Jira ticket ${ticket}..."
        local raw summary issue_type
        if command -v timeout >/dev/null 2>&1; then
            raw=$(timeout 15 jira issue view "$ticket" --raw 2>/dev/null) || raw=""
        else
            raw=$(jira issue view "$ticket" --raw 2>/dev/null) || raw=""
        fi
        if [[ -n "$raw" ]]; then
            summary=$(echo "$raw" | jq -r '.fields.summary // empty' 2>/dev/null)
            issue_type=$(echo "$raw" | jq -r '.fields.issuetype.name // empty' 2>/dev/null)
            if [[ -n "$summary" ]]; then
                mkdir -p .claude
                printf '\n## Current Work\n- **Jira:** [%s](https://redhat.atlassian.net/browse/%s)\n- **Summary:** %s\n- **Type:** %s\n' \
                    "$ticket" "$ticket" "$summary" "${issue_type:-Unknown}" >> .claude/CLAUDE.md
                echo "Appended Jira context to .claude/CLAUDE.md"
            else
                echo "Warning: Jira ticket ${ticket} has no summary field." >&2
            fi
        else
            echo "Warning: could not fetch Jira ticket ${ticket} (is jira CLI configured?)" >&2
        fi
    fi

    echo ""
    echo "Worktree ready at: ${target_dir}"
    echo "  Branch: ${branch_name}"
    echo "  To return: cd ${repo_root}"
}
