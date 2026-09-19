"""Unit tests for bot-authored PR attribution resolution."""

import unittest

from attribution import resolve_effective_author


class TestResolveEffectiveAuthor(unittest.TestCase):
    """Tests for resolve_effective_author covering all attribution scenarios."""

    def test_human_author_returned_unchanged(self):
        """A regular human author is returned as-is."""
        result = resolve_effective_author("alice", "Some PR body text")
        self.assertEqual(result, "alice")

    def test_human_author_with_attribution_pattern_unchanged(self):
        """A non-bot author is never overridden, even if body matches."""
        body = "@bob requested in [Slack thread](https://slack.com/thread)"
        result = resolve_effective_author("alice", body)
        self.assertEqual(result, "alice")

    def test_bot_with_requested_in_attribution(self):
        """Bot with '@username requested in ...' -> returns username."""
        body = "@janboll requested in [Slack thread](https://slack.com/t/123)"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "janboll")

    def test_bot_with_requested_via_attribution(self):
        """Bot with '@username requested via ...' -> returns username."""
        body = "@raelga requested via Chai Bot"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "raelga")

    def test_bot_without_attribution_returns_bot(self):
        """Bot PR without attribution pattern -> returns bot name."""
        body = "Automated dependency update."
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "redhat-chai-bot")

    def test_bot_with_empty_body_returns_bot(self):
        """Bot PR with empty body -> returns bot name."""
        result = resolve_effective_author("redhat-chai-bot", "")
        self.assertEqual(result, "redhat-chai-bot")

    def test_bot_name_case_insensitive(self):
        """Bot name matching is case-insensitive."""
        body = "@alice requested in [thread](https://example.com)"
        result = resolve_effective_author("Redhat-Chai-Bot", body)
        self.assertEqual(result, "alice")

    def test_username_with_hyphens(self):
        """Usernames containing hyphens are matched correctly."""
        body = "@my-cool-user requested in [thread](https://example.com)"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "my-cool-user")

    def test_username_with_underscores(self):
        """Usernames containing underscores are matched correctly."""
        body = "@user_name requested via Slack"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "user_name")

    def test_first_attribution_wins(self):
        """When multiple attributions exist, the first one wins."""
        body = (
            "@alice requested in [thread1](https://example.com)\n"
            "@bob requested via Chai Bot"
        )
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "alice")

    def test_attribution_in_multiline_body(self):
        """Attribution is found even when buried in a longer body."""
        body = (
            "## Summary\n"
            "This PR updates the config.\n"
            "\n"
            "---\n"
            "@engineer123 requested in [Slack](https://slack.com/t/456)\n"
            "\n"
            "Some trailing text."
        )
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "engineer123")

    def test_unknown_bot_not_matched(self):
        """A bot login not in the known set is treated like a human."""
        body = "@someone requested in [thread](https://example.com)"
        result = resolve_effective_author("some-other-bot", body)
        self.assertEqual(result, "some-other-bot")

    def test_quoted_attribution_not_matched(self):
        """Quoted attribution (e.g. '> @alice requested in ...') must not match."""
        body = "> @alice requested in [Slack thread](https://slack.com/t/123)"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "redhat-chai-bot")

    def test_indented_attribution_not_matched(self):
        """Indented attribution (e.g. '  @alice requested in ...') must not match."""
        body = "  @alice requested in [Slack thread](https://slack.com/t/123)"
        result = resolve_effective_author("redhat-chai-bot", body)
        self.assertEqual(result, "redhat-chai-bot")


if __name__ == "__main__":
    unittest.main()
