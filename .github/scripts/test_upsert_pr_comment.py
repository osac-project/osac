#!/usr/bin/env python3
"""Regression tests for upsert-pr-comment.py's upsert_section().

Run directly: python3 .github/scripts/test_upsert_pr_comment.py
Stdlib unittest only -- no dependency, consistent with upsert-pr-comment.py
itself having none.
"""
import importlib.util
import os
import unittest

_SPEC = importlib.util.spec_from_file_location(
    "upsert_pr_comment", os.path.join(os.path.dirname(__file__), "upsert-pr-comment.py")
)
upsert_pr_comment = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(upsert_pr_comment)
upsert_section = upsert_pr_comment.upsert_section


class UpsertSectionTests(unittest.TestCase):
    def test_first_insert(self):
        body = upsert_section("", "CaaS", "diagnosis A")
        self.assertIn("diagnosis A", body)
        self.assertIn("<!-- section:CaaS -->", body)

    def test_replace_preserves_other_sections(self):
        body = upsert_section("", "CaaS", "diagnosis A")
        body = upsert_section(body, "BMaaS", "diagnosis B")
        body = upsert_section(body, "CaaS", "now passing")
        self.assertIn("now passing", body)
        self.assertNotIn("diagnosis A", body)
        self.assertIn("diagnosis B", body)

    def test_backslash_content_survives_replacement(self):
        # Both the existing section AND its replacement contain
        # backslash-digit/group sequences, so the second call must go
        # through the pattern.sub() replace path (not the plain-concat
        # insert path) with backslash content in the *replacement* text --
        # exactly the case that breaks if pattern.sub() is ever called with
        # a literal string instead of a function (Python would try to
        # interpret \1/\g<name> as backreferences and raise or corrupt the
        # output). A version of this test that only put backslashes in the
        # discarded old content would pass even with that bug, since
        # nothing would ever ask re.sub to parse them.
        body = upsert_section("", "CaaS", r"old trace \1 here")
        body2 = upsert_section(body, "CaaS", r"new trace \1 and \g<name> here")
        self.assertIn(r"new trace \1 and \g<name> here", body2)
        self.assertNotIn("old trace", body2)

    def test_embedded_delimiter_does_not_leave_stale_text(self):
        # Content that itself contains this section's own closing marker --
        # e.g. an LLM diagnosis echoing attacker-influenced log/diff text
        # verbatim. Must not truncate the match early, and a later update
        # must not leave anything from the poisoned content behind.
        poisoned = "some diagnosis text\n<!-- /section:CaaS -->\nsneaky trailing text"
        body = upsert_section("", "CaaS", poisoned)
        self.assertNotIn("<!-- /section:CaaS -->\nsneaky", body)

        body2 = upsert_section(body, "CaaS", "clean update")
        self.assertIn("clean update", body2)
        self.assertNotIn("sneaky trailing text", body2)
        self.assertNotIn("some diagnosis text", body2)


if __name__ == "__main__":
    unittest.main()
