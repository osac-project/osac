# Shared by Helm render tests that assert invalid values fail with a specific error.
assert_helm_failure() {
	local expected=$1
	shift
	if "$@" >/dev/null 2>"$HELM_ERROR_FILE"; then
		printf 'Helm unexpectedly accepted invalid values; expected: %s\n' "$expected" >&2
		return 1
	fi
	if ! rg -Fq "$expected" "$HELM_ERROR_FILE"; then
		printf 'Helm failed without the expected error: %s\n' "$expected" >&2
		cat "$HELM_ERROR_FILE" >&2
		return 1
	fi
}
