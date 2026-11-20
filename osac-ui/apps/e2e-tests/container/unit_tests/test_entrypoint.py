"""Test the container entrypoint's artifact export and exit status."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ENTRYPOINT = Path(__file__).resolve().parents[1] / "entrypoint.sh"


class EntrypointTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.ui = root / "ui"
        scripts = self.ui / "apps/e2e-tests/run_scripts"
        scripts.mkdir(parents=True)
        (scripts / "run-ui-e2e.sh").write_text(
            'if [[ "$CHECK_CLEARED" == 1 ]]; then\n'
            '  [[ ! -e "$OSAC_UI_E2E_OUTPUT/playwright-report" && '
            '! -e "$OSAC_UI_E2E_OUTPUT/test-results" ]] || exit 42\n'
            'fi\n'
            'exit "$RUNNER_EXIT"\n'
        )
        for name in ("playwright-report", "test-results"):
            source = self.ui / "apps/playwright" / name
            source.mkdir(parents=True)
            (source / "artifact.txt").write_text(name)
        self.output = root / "output"
        self.output.mkdir()
        self.bin = root / "bin"
        self.bin.mkdir()
        cp = self.bin / "cp"
        cp.write_text(
            '#!/usr/bin/env bash\n'
            'printf "%s\\n" "$3" >> "$COPY_LOG"\n'
            'if [[ "$FAIL_COPY" == all || ( -n "$FAIL_COPY" && "$3" == *"/$FAIL_COPY" ) ]]; then\n'
            '  exit 23\n'
            'fi\n'
            'exec /usr/bin/cp "$@"\n'
        )
        cp.chmod(0o755)
        rm = self.bin / "rm"
        rm.write_text(
            '#!/usr/bin/env bash\n'
            'if [[ -n "$FAIL_CLEAR" && "$3" == *"/$FAIL_CLEAR" ]]; then exit 23; fi\n'
            'exec /usr/bin/rm "$@"\n'
        )
        rm.chmod(0o755)
        self.copy_log = root / "copies"

    def run_entrypoint(self, runner_exit=0, fail_copy="", fail_clear="", check_cleared=False):
        env = os.environ.copy()
        env.update(
            PATH=f"{self.bin}:{env['PATH']}",
            OSAC_UI_DIR=str(self.ui),
            OSAC_UI_E2E_OUTPUT=str(self.output),
            RUNNER_EXIT=str(runner_exit),
            FAIL_COPY=fail_copy,
            FAIL_CLEAR=fail_clear,
            CHECK_CLEARED="1" if check_cleared else "0",
            COPY_LOG=str(self.copy_log),
        )
        return subprocess.run(
            ["bash", str(ENTRYPOINT)], env=env, text=True, capture_output=True, check=False
        )

    def test_successful_copies_preserve_runner_status(self):
        for runner_exit in (0, 7):
            with self.subTest(runner_exit=runner_exit):
                result = self.run_entrypoint(runner_exit)
                self.assertEqual(result.returncode, runner_exit, result.stderr)
                for name in ("playwright-report", "test-results"):
                    self.assertEqual((self.output / name / "artifact.txt").read_text(), name)

    def test_copy_failure_is_reported_and_other_artifacts_are_attempted(self):
        for failed in ("playwright-report", "test-results", "all"):
            for runner_exit in (0, 7):
                with self.subTest(failed=failed, runner_exit=runner_exit):
                    self.copy_log.unlink(missing_ok=True)
                    result = self.run_entrypoint(runner_exit, failed)
                    self.assertEqual(result.returncode, runner_exit or 1, result.stderr)
                    copied = self.copy_log.read_text().splitlines()
                    self.assertEqual(len(copied), 2)
                    for name in ("playwright-report", "test-results"):
                        if failed in (name, "all"):
                            self.assertIn(f"Failed to copy {name} artifacts.", result.stderr)
                        else:
                            self.assertEqual((self.output / name / "artifact.txt").read_text(), name)

    def test_clears_stale_reports_before_runner_when_no_new_reports_exist(self):
        for name in ("playwright-report", "test-results"):
            source = self.ui / "apps/playwright" / name
            (source / "artifact.txt").unlink()
            source.rmdir()
            destination = self.output / name
            destination.mkdir()
            (destination / "stale.txt").write_text("old")
        result = self.run_entrypoint(7, check_cleared=True)
        self.assertEqual(result.returncode, 7, result.stderr)
        for name in ("playwright-report", "test-results"):
            self.assertFalse((self.output / name).exists())

    def test_clear_failure_preserves_runner_status_and_does_not_copy_over_stale_data(self):
        destination = self.output / "playwright-report"
        destination.mkdir()
        (destination / "stale.txt").write_text("old")
        for runner_exit in (0, 7):
            with self.subTest(runner_exit=runner_exit):
                result = self.run_entrypoint(runner_exit, fail_clear="playwright-report")
                self.assertEqual(result.returncode, runner_exit or 1, result.stderr)
                self.assertIn("Failed to clear playwright-report artifacts.", result.stderr)
                self.assertTrue((destination / "stale.txt").exists())
                self.assertFalse((destination / "artifact.txt").exists())
                self.assertEqual(
                    (self.output / "test-results/artifact.txt").read_text(), "test-results"
                )


if __name__ == "__main__":
    unittest.main()
