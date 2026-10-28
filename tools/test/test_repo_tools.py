"""Check that shared developer tools resolve inside the repository.

Run: python3 tools/test/test_repo_tools.py
"""

import re
import subprocess
import unittest

import yaml
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class RepoToolsTest(unittest.TestCase):
    def test_golangci_lint_lives_in_the_ignored_tools_bin(self):
        """A repo-root bin/ is not gitignored; the shared cache is tools/bin."""
        result = subprocess.run(
            ["make", "-f", "tools/golangci-lint.mk", "-p", "-n"],
            cwd=ROOT, capture_output=True, text=True, timeout=60,
        )
        tools_dir = re.search(r"^GOLANGCI_LINT_TOOLS_DIR := (.+)$", result.stdout, re.M)
        self.assertIsNotNone(tools_dir, result.stdout[-2000:] + result.stderr[-2000:])
        self.assertEqual(tools_dir.group(1).strip(), str(ROOT / "tools"))
        binary = re.search(r"^GOLANGCI_LINT_BIN = (.+)$", result.stdout, re.M)
        self.assertIsNotNone(binary)
        self.assertEqual(binary.group(1).strip(), "$(GOLANGCI_LINT_TOOLS_DIR)/bin")


    def containerfile_arg(self, name):
        match = re.search(rf"^ARG {name}=(\S+)$",
                          (ROOT / "tools/carbonite/Containerfile").read_text(), re.M)
        self.assertIsNotNone(match, name)
        return match.group(1)

    def test_containerfile_go_version_matches_the_workspace(self):
        workspace = re.search(r"^go (\S+)$", (ROOT / "go.work").read_text(), re.M)
        self.assertIsNotNone(workspace)
        self.assertEqual(self.containerfile_arg("GO_VERSION"), workspace.group(1))

    def test_containerfile_ginkgo_matches_the_modules(self):
        module = re.search(r"github\.com/onsi/ginkgo/v2 (v\S+)",
                           (ROOT / "osac-operator/go.mod").read_text())
        self.assertIsNotNone(module)
        self.assertEqual("v" + self.containerfile_arg("GINKGO_VERSION"), module.group(1))

    def test_claude_code_version_is_pinned(self):
        self.assertRegex(self.containerfile_arg("CLAUDE_CODE_VERSION"), r"^\d+\.\d+\.\d+$")
        text = (ROOT / "tools/carbonite/Containerfile").read_text()
        self.assertIn("@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}", text)

    def test_envtest_wrapper_writes_no_files_to_the_checkout(self):
        text = (ROOT / "osac-operator/hack/go-test-openshell.sh").read_text()
        self.assertNotIn('--tmpdir="$PWD"', text)

    def test_policy_rules_allow_the_project_python_interpreter(self):
        """The image's venv runs /usr/bin/python3.13, not /usr/bin/python3."""
        policies = yaml.safe_load(
            (ROOT / "tools/carbonite/policy.yaml").read_text())["network_policies"]
        missing = [
            name for name, rule in policies.items()
            if "/usr/bin/python3" in [b["path"] for b in rule.get("binaries", [])]
            and "/usr/bin/python3.13" not in [b["path"] for b in rule.get("binaries", [])]
        ]
        self.assertEqual([], missing)

    def test_policy_paths_are_independent_of_the_default_workdir(self):
        """CARBONITE_WORKDIR is documented as overridable; no path may assume it."""
        text = (ROOT / "tools/carbonite/policy.yaml").read_text()
        self.assertNotIn("/sandbox/osac", text)

if __name__ == "__main__":
    unittest.main()
