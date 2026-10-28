#!/usr/bin/env bash
# The shipped rule revision 0001 must never change after release. The ConfigMap
# is immutable, the admission policy blocks deleting it, and metering refuses
# to start when its database record no longer matches the file, so an edit to a
# released revision wedges every upgrade on every installed cluster. A rule
# change ships as the next revision number, never as an edit of 0001; see the
# append-only contract in charts/osac-metering/templates/rules.yaml.
set -euo pipefail

cd "$(dirname "$0")/../.."

file="osac-metering/charts/osac-metering/rules/0001-meters.yaml"

[[ -f "$file" ]] || { echo "ERROR: $file does not exist." >&2; exit 1; }

# The most recent tag that ships the rules file, by the tag's creation date
# (the tagger date of an annotated tag, the commit date of a lightweight one).
# Mixed tag families (osac/v*, osac-metering/v*) make version sort unreliable,
# so the newest tag wins.
latest_with_file() {
	local tag
	while IFS= read -r tag; do
		if git cat-file -e "$tag:$file" 2>/dev/null; then
			echo "$tag"
			return 0
		fi
	done < <(git for-each-ref 'refs/tags' --sort=-creatordate --format='%(refname:short)')
	return 0
}

latest="$(latest_with_file)"

if [[ -z "$latest" ]]; then
	# A shallow CI checkout has no tags: try to fetch them, and pass if that is
	# not possible — the check is a release gate, not a network dependency.
	if git fetch --tags --quiet 2>/dev/null; then
		latest="$(latest_with_file)"
	fi
fi

if [[ -z "$latest" ]]; then
	echo "No released tag ships $file yet; nothing to compare (first release pending)."
	exit 0
fi

if git diff --quiet "$latest" -- "$file"; then
	echo "Rule revision 0001 is unchanged since $latest."
	exit 0
fi

echo "ERROR: $file changed since the released tag $latest." >&2
echo "" >&2
echo "A released rule revision is immutable history:" >&2
echo "  - Kubernetes rejects any update to the immutable ConfigMap," >&2
echo "  - the admission policy blocks deleting it," >&2
echo "  - metering refuses to start when its database record no longer matches." >&2
echo "" >&2
echo "Ship the change as the next revision number instead: copy the file to" >&2
echo "0002-meters.yaml, give it a later effective_at, and leave 0001 as it was." >&2
exit 1
