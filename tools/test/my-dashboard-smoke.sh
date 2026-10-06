#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT

cat > "$test_dir/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
    "api user") echo 'tester' ;;
    "api orgs/"*) ;;
    "api graphql") echo '[]' ;;
    "pr list") echo '[]' ;;
    *) exit 1 ;;
esac
EOF

cat > "$test_dir/jira" <<'EOF'
#!/usr/bin/env bash
if [ "$1 $2" != 'issue list' ]; then exit 1; fi
echo '[{"key":"OSAC-1234","fields":{"summary":"Dashboard test task","status":{"name":"In Progress"},"priority":{"name":"High"},"issueType":{"name":"Task"}}}]'
EOF

cat > "$test_dir/curl" <<'EOF'
#!/usr/bin/env bash
if [ "$MOCK_CURL_MODE" = 'unavailable' ]; then exit 26; fi
echo '{"values":[{"state":"active","name":"Test sprint","startDate":"2026-09-20T00:00:00Z","endDate":"2026-10-04T00:00:00Z"}]}'
EOF

chmod +x "$test_dir/gh" "$test_dir/jira" "$test_dir/curl"

run_dashboard() {
    if ! PATH="$test_dir:$PATH" MOCK_CURL_MODE="$1" \
        bash "$repo_root/tools/my-dashboard.sh" > "$test_dir/output" 2> "$test_dir/error"; then
        cat "$test_dir/error" >&2
        echo "Dashboard failed with curl mode: $1" >&2
        exit 1
    fi
}

assert_output() {
    if ! grep -Fq "$1" "$test_dir/output"; then
        echo "Dashboard output missing: $1" >&2
        exit 1
    fi
}

run_dashboard unavailable
assert_output 'OSAC-1234'
assert_output 'Sprint dates unavailable from Jira board API.'
assert_output 'External Contributor PRs'
if grep -Fq 'Sprint timeline' "$test_dir/output"; then
    echo 'Dashboard showed a sprint timeline without sprint dates' >&2
    exit 1
fi

run_dashboard available
assert_output 'Test sprint'
assert_output 'Sprint timeline'
assert_output 'OSAC-1234'
assert_output 'External Contributor PRs'

echo 'my-dashboard smoke tests passed'
