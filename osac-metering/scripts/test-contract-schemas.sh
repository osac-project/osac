#!/usr/bin/env bash
# A published schema file never changes after release. A reader holds the file and validates
# records against it, and metering validates a record's named version and then decodes it with
# the current add-only payload shape, so an edit would re-read old records with a changed
# shape and bill them wrong. A field change ships as the next version file, never as an edit
# of a published one. This mirrors the rule-revision check in test-rules-unchanged.sh.
set -euo pipefail

cd "$(dirname "$0")/../.."

directory="osac-metering/schema/versions"

[[ -d "$directory" ]] || { echo "ERROR: $directory does not exist." >&2; exit 1; }

# The most recent tag that ships the published schema files, by the tag's creation date
# (the tagger date of an annotated tag, the commit date of a lightweight one). Mixed tag
# families (osac/v*, osac-metering/v*) make version sort unreliable, so the newest tag wins.
latest_with_directory() {
	local tag
	while IFS= read -r tag; do
		if git cat-file -e "$tag:$directory" 2>/dev/null; then
			echo "$tag"
			return 0
		fi
	done < <(git for-each-ref 'refs/tags' --sort=-creatordate --format='%(refname:short)')
	return 0
}

latest="$(latest_with_directory)"

if [[ -z "$latest" ]]; then
	# A shallow CI checkout has no tags: try to fetch them, and pass if that is not
	# possible — the check is a release gate, not a network dependency.
	if git fetch --tags --quiet 2>/dev/null; then
		latest="$(latest_with_directory)"
	fi
fi

if [[ -z "$latest" ]]; then
	echo "No released tag ships $directory yet; nothing to compare (first release pending)."
	exit 0
fi

# Every file the released tag publishes must still exist with identical bytes. A new version
# file is allowed: it is the only way a field change ships.
changed=0
while IFS= read -r file; do
	[[ -n "$file" ]] || continue
	if ! git diff --quiet "$latest" -- "$file"; then
		echo "ERROR: $file changed, was renamed, or was deleted since $latest." >&2
		changed=1
	fi
done < <(git ls-tree -r --name-only "$latest" -- "$directory")

if [[ "$changed" -eq 0 ]]; then
	echo "Published schema files are unchanged since $latest."
	exit 0
fi

cat >&2 <<'EOF'

A published schema version is immutable history:
  - a reader holds the file and validates records against it,
  - metering replays retained records with the version in effect when they were written,
  - a changed field would read old records with a new shape and bill them wrong.

Ship the change as the next version file instead: copy the latest file to the next version
number, give it the next version, a later effective_at, and the payload shape metering now
sends, then leave the published file as it was. Renaming or deleting a version needs the
retirement work the design reserves for when its records are gone.
EOF
exit 1
