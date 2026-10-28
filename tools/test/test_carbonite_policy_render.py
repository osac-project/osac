"""Exercise tools/carbonite/render-policy.sh without a gateway.

Run: python3 tools/test/test_carbonite_policy_render.py
"""

import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
RENDER = REPO_ROOT / "tools/carbonite/render-policy.sh"

POLICY = """\
network_policies:
  gitlab:
    endpoints:
      - host: gitlab.internal.example
      - host: gitlab.internal.example
  jira:
    endpoints:
      - host: jira.internal.example
      - host: api.atlassian.com
  rh_pattern_server:
    endpoints:
      - host: patterns.internal.example
"""


class RenderPolicyTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="osac-carb-render-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / "policy.yaml"
        self.source.write_text(POLICY)
        self.dest = self.root / "state" / "policy-rendered.yaml"
        self.env = {
            key: value for key, value in os.environ.items()
            if not key.startswith("CARBONITE_")
        }

    def render(self, *, succeeds=True, **env):
        result = subprocess.run(
            [str(RENDER), str(self.source), str(self.dest)],
            env={**self.env, **env}, capture_output=True, text=True,
        )
        if succeeds:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_substitutes_all_three_hosts(self):
        self.render(
            CARBONITE_JIRA_HOST="redhat.atlassian.net",
            CARBONITE_GITLAB_HOST="gitlab.corp.test",
            CARBONITE_PATTERNS_HOST="patterns.corp.test",
        )
        rendered = self.dest.read_text()
        self.assertIn("host: redhat.atlassian.net", rendered)
        self.assertEqual(rendered.count("host: gitlab.corp.test"), 2)
        self.assertIn("host: patterns.corp.test", rendered)
        self.assertNotIn(".internal.example", rendered)
        self.assertIn("host: api.atlassian.com", rendered)

    def test_unset_hosts_keep_their_placeholders(self):
        self.render(CARBONITE_JIRA_HOST="redhat.atlassian.net")
        rendered = self.dest.read_text()
        self.assertIn("host: redhat.atlassian.net", rendered)
        self.assertIn("gitlab.internal.example", rendered)
        self.assertIn("patterns.internal.example", rendered)

    def test_empty_values_count_as_unset(self):
        self.render(CARBONITE_JIRA_HOST="")
        self.assertEqual(self.dest.read_text(), POLICY)

    def test_source_is_never_modified(self):
        self.render(CARBONITE_JIRA_HOST="redhat.atlassian.net")
        self.assertEqual(self.source.read_text(), POLICY)

    def test_rendered_policy_is_private(self):
        self.render(CARBONITE_JIRA_HOST="redhat.atlassian.net")
        self.assertEqual(stat.S_IMODE(self.dest.stat().st_mode), 0o600)

    def test_rejects_values_that_are_not_plain_hostnames(self):
        for value in (
            "a/b", "a b", "a;b", "a&b", "a\\b", "a\nb", "a:443",
            "-a.example", ".a.example", "a..example", "a.example.",
            "*.example", "a|b",
        ):
            with self.subTest(value=value):
                result = self.render(succeeds=False, CARBONITE_JIRA_HOST=value)
                self.assertIn("CARBONITE_JIRA_HOST", result.stderr)
                self.assertFalse(self.dest.exists())

    def test_warns_when_the_placeholder_is_absent(self):
        self.source.write_text("network_policies: {}\n")
        result = self.render(CARBONITE_JIRA_HOST="redhat.atlassian.net")
        self.assertIn("jira.internal.example", result.stderr)

    def test_missing_source_fails(self):
        self.source.unlink()
        result = self.render(succeeds=False, CARBONITE_JIRA_HOST="a.example")
        self.assertIn("not found", result.stderr)

    def test_refuses_to_render_inside_the_checkout(self):
        self.dest = REPO_ROOT / ".tmp" / "should-not-exist.yaml"
        result = self.render(succeeds=False, CARBONITE_JIRA_HOST="a.example")
        self.assertIn("outside the checkout", result.stderr)
        self.assertFalse(self.dest.exists())


if __name__ == "__main__":
    unittest.main()
