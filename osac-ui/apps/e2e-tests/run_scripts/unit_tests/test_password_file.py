"""Check the UI runner's password inputs without a deployed cluster."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


UI_DIR = Path(__file__).resolve().parents[4]
RUNNER = UI_DIR / "apps/e2e-tests/run_scripts/run-ui-e2e.sh"
TEST_PASSWORD = "test password"


class PasswordFileTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.password_file = self.root / "password"
        self.password_file.write_text(TEST_PASSWORD + "\n")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.stub("curl", "printf 200")
        self.stub(
            "pnpm",
            'if [[ " $* " == *" test "* ]]; then '
            '[[ "$OSAC_ADMIN_PASSWORD" == "$EXPECTED_PASSWORD" ]]; fi',
        )

    def stub(self, name, body):
        path = self.bin / name
        path.write_text(f"#!/usr/bin/env bash\n{body}\n")
        path.chmod(0o755)

    def run_runner(self, *args, password_env=None):
        env = os.environ.copy()
        env.pop("OSAC_ADMIN_PASSWORD", None)
        env.update(
            PATH=f"{self.bin}:{env['PATH']}",
            OSAC_UI_BASE_URL="http://localhost:5173",
            EXPECTED_PASSWORD=TEST_PASSWORD,
        )
        if password_env is not None:
            env["OSAC_ADMIN_PASSWORD"] = password_env
        return subprocess.run(
            ["bash", str(RUNNER), "--project=smoke", "--allow-root-e2e", *args],
            cwd=UI_DIR,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def test_file_password_is_passed_to_playwright(self):
        for args in (
            (f"--admin-password-file={self.password_file}",),
            ("--admin-password-file", str(self.password_file)),
        ):
            with self.subTest(args=args):
                result = self.run_runner(*args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertNotIn(TEST_PASSWORD, result.stdout + result.stderr)

    def test_password_option_remains_supported(self):
        result = self.run_runner(f"--admin-password={TEST_PASSWORD}")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_missing_or_empty_file(self):
        empty = self.root / "empty"
        empty.touch()
        for args in (
            ("--admin-password-file",),
            ("--admin-password-file=",),
            (f"--admin-password-file={self.root / 'missing'}",),
            (f"--admin-password-file={empty}",),
        ):
            with self.subTest(args=args):
                result = self.run_runner(*args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("password", result.stderr.lower())

    def test_rejects_conflicting_sources_in_either_order(self):
        file_arg = f"--admin-password-file={self.password_file}"
        direct_arg = f"--admin-password={TEST_PASSWORD}"
        for args, password_env in (
            ((file_arg, direct_arg), None),
            ((direct_arg, file_arg), None),
            ((file_arg,), TEST_PASSWORD),
        ):
            with self.subTest(args=args, password_env=password_env is not None):
                result = self.run_runner(*args, password_env=password_env)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("only one admin password source", result.stderr.lower())
                self.assertNotIn(TEST_PASSWORD, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
