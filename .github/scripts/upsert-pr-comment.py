#!/usr/bin/env python3
"""Insert or replace a named, HTML-comment-delimited section within a
sticky PR comment body.

Lets ai-diagnostic-e2e.yml maintain one persistent comment per PR (one
section per E2E suite) instead of posting a new comment on every failure.
Pure text transform -- the calling workflow step handles all GitHub API
calls (finding the existing comment, POSTing/PATCHing the result).

SECTION_KEY is the triggering workflow's own name and is nominally
re-nameable by a PR that edits its own caller workflow's `name:` field --
but it's regex-escaped before use here and only ever affects that PR's own
single sticky comment, so at worst a renamed section label is cosmetically
confusing within that one PR, never a cross-PR or injection issue.
"""
import os
import re
import sys

COMMENT_MARKER = "<!-- osac-ai-diagnostic-comment -->"
# Matches any of our own marker shapes so they can be neutralized if they
# ever show up *inside* section content (see upsert_section below).
_MARKER_RE = re.compile(r"<!--\s*/?section:.*?-->", re.DOTALL)


def upsert_section(body, key, content):
    start = f"<!-- section:{key} -->"
    end = f"<!-- /section:{key} -->"
    # content (e.g. an LLM diagnosis) can echo attacker-influenced log/diff
    # text verbatim. If it happens to contain a literal copy of `end` (or
    # any other section marker), a later non-greedy regex search below
    # would terminate at that embedded copy instead of the real delimiter,
    # truncating the match and leaving stale content behind on the next
    # update. Neutralize any embedded marker-shaped text before it's ever
    # written into the body -- rendered as inert literal text, not parsed.
    safe_content = _MARKER_RE.sub(
        lambda m: m.group(0).replace("<!--", "&lt;!--"), content.strip()
    )
    block = f"{start}\n{safe_content}\n{end}"
    pattern = re.compile(re.escape(start) + r".*?" + re.escape(end), re.DOTALL)
    if pattern.search(body):
        # Replacement via a function, not a literal string, so backslash
        # sequences in the diagnosis text (e.g. "\1" inside a stack trace)
        # are never interpreted as regex backreferences.
        return pattern.sub(lambda _match: block, body, count=1)
    sep = "\n\n" if body.strip() else "\n"
    return body.rstrip() + sep + block + "\n"


def main():
    current_body_path = os.environ["CURRENT_BODY_FILE"]
    section_key = os.environ["SECTION_KEY"]
    section_content_path = os.environ["SECTION_CONTENT_FILE"]
    output_path = os.environ["OUTPUT_BODY_FILE"]

    current_body = ""
    if os.path.isfile(current_body_path):
        with open(current_body_path, "r") as f:
            current_body = f.read()
    if not current_body.strip():
        current_body = COMMENT_MARKER + "\n"
    elif COMMENT_MARKER not in current_body:
        # Shouldn't happen (we only ever PATCH comments found via the
        # marker), but stay safe if the comment was ever hand-edited.
        current_body = COMMENT_MARKER + "\n" + current_body

    with open(section_content_path, "r") as f:
        section_content = f.read()

    new_body = upsert_section(current_body, section_key, section_content)

    with open(output_path, "w") as f:
        f.write(new_body)


if __name__ == "__main__":
    sys.exit(main())
