#!/usr/bin/env bash
# Smoke tests for root Claude/Codex component hook routing and PR safeguards.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
HOOK="${REPO_ROOT}/.claude/hooks/component-pre-tool-use.sh"
CODEX_POST_HOOK="${REPO_ROOT}/.codex/hooks/component-post-tool-use.sh"
COMMON_POST_HOOK="${REPO_ROOT}/.claude/hooks/component-post-tool-use.sh"
CLAUDE_CONFIG="${REPO_ROOT}/.claude/settings.json"
CODEX_CONFIG="${REPO_ROOT}/.codex/hooks.json"
TEST_ROOT=$(mktemp -d)
BIN="${TEST_ROOT}/bin"
LOG="${TEST_ROOT}/commands.log"
mkdir -p "$BIN"
touch "$LOG"
trap 'rm -rf "$TEST_ROOT"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$HOOK" ]] || fail "missing $HOOK"
[[ -f "$CODEX_POST_HOOK" ]] || fail "missing $CODEX_POST_HOOK"
command -v git >/dev/null 2>&1 || fail "git not on PATH"
command -v jq >/dev/null 2>&1 || fail "jq not on PATH"

cat >"${BIN}/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'make|%s\n' "$*" >>"$HOOK_COMMAND_LOG"
if [[ -n "${OPERATOR_TEST_GENERATE:-}" && "${!#}" == test \
  && "${2:-}" == "${OPERATOR_TEST_DIR:-}" ]]; then
  mkdir -p "$(dirname "$OPERATOR_TEST_GENERATE")"
  printf 'generated: true\n' >"$OPERATOR_TEST_GENERATE"
fi
EOF

cat >"${BIN}/gofmt" <<'EOF'
#!/usr/bin/env bash
printf 'gofmt|%s\n' "$*" >>"$HOOK_COMMAND_LOG"
EOF

cat >"${BIN}/ginkgo" <<'EOF'
#!/usr/bin/env bash
printf 'ginkgo|%s\n' "$*" >>"$HOOK_COMMAND_LOG"
EOF

cat >"${BIN}/go" <<'EOF'
#!/usr/bin/env bash
printf 'go|%s|%s\n' "$*" "$PWD" >>"$HOOK_COMMAND_LOG"
EOF

cat >"${BIN}/gh" <<'EOF'
#!/usr/bin/env bash
if [[ "${GH_FAIL:-0}" == 1 ]]; then
  exit 1
fi
printf 'osac-project/osac\tmain\n'
EOF

chmod +x "${BIN}/make" "${BIN}/gofmt" "${BIN}/ginkgo" "${BIN}/go" "${BIN}/gh"
export PATH="${BIN}:${PATH}"
export HOOK_COMMAND_LOG="$LOG"

# Clear the stub command log before each independent scenario.
clear_log() { : >"$LOG"; }

# Create a fixture repository with an upstream base ref for PR path resolution.
init_repo() {
  local repo=$1
  mkdir -p "$repo/proto/private" "$repo/fulfillment-service/internal" "$repo/osac-operator"
  git -C "$repo" init -q -b main
  git -C "$repo" config user.email hooks-test@example.invalid
  git -C "$repo" config user.name hooks-test
  printf 'syntax = "proto3";\n' >"$repo/proto/private/base.proto"
  printf 'package service\n' >"$repo/fulfillment-service/main.go"
  printf 'package internal\n' >"$repo/fulfillment-service/internal/main.go"
  printf 'package operator\n' >"$repo/osac-operator/main.go"
  git -C "$repo" add proto fulfillment-service osac-operator
  git -C "$repo" commit -q -m seed
  git -C "$repo" remote add origin https://github.com/osac-project/osac.git
  git -C "$repo" update-ref refs/remotes/origin/main HEAD
  git -C "$repo" checkout -q -b feature
}

# Send a serialized Bash PreToolUse event to the component hook.
run_hook() {
  local repo=$1 command=$2
  jq -nc --arg cwd "$repo" --arg command "$command" \
    '{cwd:$cwd,tool_input:{command:$command}}' \
    | bash "$HOOK"
}

# Require a specific stub command to have been invoked.
assert_log_contains() {
  local expected=$1
  [[ "$(cat "$LOG")" == *"$expected"* ]] || fail "expected command log to contain: $expected"
}

# Require a stub command to appear an exact number of times.
assert_log_count() {
  local expected=$1 count=$2 actual
  actual=$(awk -v expected="$expected" '$0 == expected { count++ } END { print count + 0 }' "$LOG")
  [[ "$actual" == "$count" ]] || fail "expected '$expected' $count time(s), got $actual: $(cat "$LOG")"
}

# Assert the hook blocks the tool action with the expected validation message.
expect_hook_failure() {
  local repo=$1 command=$2 expected=$3 output rc=0
  output=$(run_hook "$repo" "$command" 2>&1) || rc=$?
  [[ "$rc" == 2 ]] || fail "expected hook exit 2, got $rc: $output"
  [[ "$output" == *"$expected"* ]] || fail "expected '$expected' in output: $output"
}

# Send Codex's native Bash PreToolUse payload to the shared component hook.
run_codex_pre_hook() {
  local repo=$1 command=$2
  jq -nc --arg cwd "$repo" --arg command "$command" \
    '{cwd:$cwd,hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$command}}' \
    | bash "$HOOK"
}

# Install the Codex adapter and shared router into a fixture repository.
install_fixture_post_hooks() {
  local repo=$1
  mkdir -p "$repo/.codex/hooks" "$repo/.claude/hooks"
  cp "$CODEX_POST_HOOK" "$repo/.codex/hooks/component-post-tool-use.sh"
  cp "$COMMON_POST_HOOK" "$repo/.claude/hooks/component-post-tool-use.sh"
}

# Send Codex's native apply_patch PostToolUse payload through its path adapter.
run_codex_post_hook() {
  local repo=$1 patch=$2
  jq -nc --arg cwd "$repo" --arg patch "$patch" \
    '{cwd:$cwd,hook_event_name:"PostToolUse",tool_name:"apply_patch",tool_input:{command:$patch},tool_response:"Success"}' \
    | bash "$repo/.codex/hooks/component-post-tool-use.sh"
}

# Send Claude's existing single-file edit payload to the shared post hook.
run_claude_post_hook() {
  local repo=$1 file_path=$2
  jq -nc --arg cwd "$repo" --arg file_path "$file_path" \
    '{cwd:$cwd,tool_input:{file_path:$file_path}}' \
    | bash "$COMMON_POST_HOOK"
}

# Confirm Claude settings register the existing shared component hooks.
test_claude_config_registration() {
  jq -e '
    any(.hooks.PreToolUse[]; .matcher == "Bash" and
      any(.hooks[]; (.command // "") | contains("component-pre-tool-use.sh")))
    and any(.hooks.PostToolUse[]; .matcher == "Edit|MultiEdit|Write" and
      any(.hooks[]; (.command // "") | contains("component-post-tool-use.sh")))
  ' "$CLAUDE_CONFIG" >/dev/null || fail "Claude settings are missing component hooks"
  pass "Claude settings register component hooks"
}

# Confirm Codex registration preserves existing hooks and wires both component paths.
test_codex_config_registration() {
  jq -e '
    any(.hooks.SessionStart[].hooks[]; (.command // "") | contains("update-ai-context.sh"))
    and any(.hooks.SessionStart[].hooks[]; (.command // "") | contains("fetch-graphify-brain.sh"))
    and any(.hooks.PreToolUse[]; .matcher == "Bash" and
      any(.hooks[]; (.command // "") | contains("graphify hook-guard search")) and
      any(.hooks[]; (.command // "") | contains("component-pre-tool-use.sh")))
    and any(.hooks.PostToolUse[]; .matcher == "apply_patch" and
      any(.hooks[]; (.command // "") | contains(".codex/hooks/component-post-tool-use.sh")))
  ' "$CODEX_CONFIG" >/dev/null || fail "Codex hooks are missing or existing hooks were dropped"
  pass "Codex config preserves existing hooks and registers component checks"
}

# Cover proto lint detection before a new proto file is added and committed.
test_untracked_proto_commit() {
  local repo="${TEST_ROOT}/untracked-proto"
  init_repo "$repo"
  printf 'message New {}\n' >"$repo/proto/private/new.proto"
  clear_log
  run_hook "$repo" "git add proto/private/new.proto && git commit -m schema"
  assert_log_contains "make|-C $repo/proto lint"
  pass "commit checks include new untracked proto files"
}

# Ensure a chained commit and PR command runs both pre-command validations.
test_chained_commit_and_pr() {
  local repo="${TEST_ROOT}/chained"
  init_repo "$repo"
  printf '\n// changed\n' >>"$repo/proto/private/base.proto"
  printf '\n// changed\n' >>"$repo/fulfillment-service/main.go"
  printf '\n// changed\n' >>"$repo/osac-operator/main.go"
  clear_log
  run_codex_pre_hook "$repo" "git add . && git commit -m changes && git push && gh pr create --repo osac-project/osac"
  assert_log_contains "make|-C $repo/proto lint"
  assert_log_contains "ginkgo|run -r internal"
  assert_log_contains "make|-C $repo/osac-operator test"
  pass "chained commit and PR commands run both validations"
}

# Cover Codex's apply_patch paths and ensure each component action runs once.
test_codex_post_tool_multi_file_patch() {
  local repo="${TEST_ROOT}/codex-post-multi"
  local patch
  init_repo "$repo"
  install_fixture_post_hooks "$repo"
  mkdir -p "$repo/proto/private/nested" "$repo/proto/tests/nested" \
    "$repo/osac-operator/api/v1alpha1/nested" "$repo/osac-operator/api"
  patch='*** Begin Patch
*** Update File: proto/private/nested/base.proto
@@
*** Delete File: proto/private/nested/removed.proto
*** Add File: proto/tests/nested/new.proto
+message New {}
*** Update File: fulfillment-service/go.mod
@@
*** Update File: osac-operator/go.mod
@@
*** Update File: osac-operator/api/go.mod
@@
*** Update File: osac-operator/api/v1alpha1/nested/first_types.go
@@
*** Update File: osac-operator/api/v1alpha1/nested/old_types.go
*** Move to: osac-operator/api/v1alpha1/nested/moved_types.go
*** Add File: osac-operator/api/v1alpha1/nested/second_types.go
+package v1alpha1
*** End Patch'
  clear_log
  run_codex_post_hook "$repo" "$patch"
  assert_log_count "make|-C $repo/proto lint" 1
  assert_log_count "make|-C $repo/proto generate" 1
  assert_log_count "go|mod tidy|$repo/fulfillment-service" 1
  assert_log_count "go|mod tidy|$repo/osac-operator" 1
  assert_log_count "go|mod tidy|$repo/osac-operator/api" 1
  assert_log_count "make|-C $repo/osac-operator manifests generate" 1
  pass "Codex patch events route nested multi-file edits once per component action"
}

# Keep Claude's existing single-path payload working through the shared router.
test_claude_post_tool_single_file() {
  local repo="${TEST_ROOT}/claude-post-single"
  init_repo "$repo"
  mkdir -p "$repo/proto/private/nested"
  clear_log
  run_claude_post_hook "$repo" "proto/private/nested/claude.proto"
  assert_log_count "make|-C $repo/proto lint" 1
  assert_log_count "make|-C $repo/proto generate" 1
  pass "Claude single-file edit payload still routes through shared post checks"
}

# Ensure unrelated patch paths do not run any component command.
test_codex_post_tool_unrelated_file() {
  local repo="${TEST_ROOT}/codex-post-unrelated"
  init_repo "$repo"
  install_fixture_post_hooks "$repo"
  clear_log
  run_codex_post_hook "$repo" $'*** Begin Patch\n*** Update File: README.md\n@@\n+unrelated\n*** End Patch'
  [[ ! -s "$LOG" ]] || fail "unrelated Codex patch triggered component commands: $(cat "$LOG")"
  pass "unrelated Codex patch files trigger no component action"
}

# Ensure failed base resolution still formats tracked and untracked service Go files.
test_fallback_formats_service_files() {
  local repo="${TEST_ROOT}/fallback"
  init_repo "$repo"
  printf '\n// changed\n' >>"$repo/fulfillment-service/main.go"
  printf 'package untracked\n' >"$repo/fulfillment-service/untracked.go"
  clear_log
  GH_FAIL=1 run_hook "$repo" "gh pr create --repo osac-project/osac"
  assert_log_contains "gofmt|-s -w $repo/fulfillment-service/main.go"
  assert_log_contains "gofmt|-s -w $repo/fulfillment-service/internal/main.go"
  assert_log_contains "gofmt|-s -w $repo/fulfillment-service/untracked.go"
  pass "base-resolution fallback formats tracked service files"
}

# Ensure generated operator changes after make test block PR creation.
test_operator_generation_drift_fails() {
  local repo="${TEST_ROOT}/operator-drift"
  init_repo "$repo"
  printf '\n// changed\n' >>"$repo/osac-operator/main.go"
  clear_log
  OPERATOR_TEST_GENERATE="$repo/osac-operator/generated-crd.yaml" \
    OPERATOR_TEST_DIR="$repo/osac-operator" \
    expect_hook_failure "$repo" "gh pr create --repo osac-project/osac" \
      "make test modified or generated osac-operator files"
  pass "untracked operator manifest generation during make test blocks PR creation"
}

test_claude_config_registration
test_codex_config_registration
test_untracked_proto_commit
test_chained_commit_and_pr
test_codex_post_tool_multi_file_patch
test_claude_post_tool_single_file
test_codex_post_tool_unrelated_file
test_fallback_formats_service_files
test_operator_generation_drift_fails
