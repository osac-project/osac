"""Check Playwright output using synthetic credentials, without a cluster."""

import base64
import io
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
import zipfile


UI_DIR = Path(__file__).resolve().parents[4]
CLI = UI_DIR / 'apps/playwright/node_modules/@playwright/test/cli.js'
CONFIG = UI_DIR / 'apps/e2e-tests/helper-tests/fixtures/password-report.config.ts'
SYNTHETIC_PASSWORD = 'synthetic-password-report-check-2461'
SYNTHETIC_REPLACEMENT = 'synthetic-replacement-report-check-2461'
DEBUG_ENV_VARS = ('DEBUG', 'PWDEBUG', 'npm_config_pwdebug', 'npm_package_config_pwdebug')


class PasswordReportTests(unittest.TestCase):
    def test_success_and_failure_do_not_expose_password(self):
        with tempfile.TemporaryDirectory(prefix='osac-password-report-') as directory:
            root = Path(directory)
            env = os.environ.copy()
            for variable in DEBUG_ENV_VARS:
                env.pop(variable, None)
            env.update(
                PASSWORD_TEST_SECRET=SYNTHETIC_PASSWORD,
                PASSWORD_TEST_REPLACEMENT=SYNTHETIC_REPLACEMENT,
                PASSWORD_TEST_OUTPUT=str(root / 'results'),
                PLAYWRIGHT_HTML_OUTPUT_DIR=str(root / 'report'),
                PLAYWRIGHT_HTML_OPEN='never',
            )
            result = subprocess.run(
                ['node', str(CLI), 'test', '--config', str(CONFIG),
                 '--reporter=line,html', '--workers=1'],
                cwd=UI_DIR,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            output = result.stdout + result.stderr
            self.assertIn('3 passed', output)
            self.assertIn('4 failed', output)
            self.assertIn('synthetic failure after password entry', output)
            self.assertIn('synthetic password input error', output)
            self.assertIn('Password field is not editable', output)
            self.assertNotIn(SYNTHETIC_PASSWORD, output)
            self.assertNotIn(SYNTHETIC_REPLACEMENT, output)
            html = (root / 'report/index.html').read_text()
            self.assertNotIn(SYNTHETIC_PASSWORD, html)
            self.assertNotIn(SYNTHETIC_REPLACEMENT, html)
            embedded = re.search(r'data:application/zip;base64,([A-Za-z0-9+/=]+)', html)
            self.assertIsNotNone(embedded, 'Missing HTML report data')
            with zipfile.ZipFile(io.BytesIO(base64.b64decode(embedded.group(1)))) as report:
                for name in report.namelist():
                    self.assertNotIn(SYNTHETIC_PASSWORD.encode(), report.read(name), name)
                    self.assertNotIn(SYNTHETIC_REPLACEMENT.encode(), report.read(name), name)
            for path in (root / 'results').rglob('*'):
                if path.is_file():
                    self.assertNotIn(SYNTHETIC_PASSWORD.encode(), path.read_bytes(), str(path))
                    self.assertNotIn(SYNTHETIC_REPLACEMENT.encode(), path.read_bytes(), str(path))

    def test_rejects_playwright_debugging_before_running(self):
        for overrides in (
            {'DEBUG': 'pw:api'},
            # Avoid verbose channel output masking the guard error on process exit.
            {'DEBUG': '*,-pw:channel'},
            {'PWDEBUG': '1'},
            {'npm_config_pwdebug': '1'},
            {'npm_package_config_pwdebug': '1'},
            {'npm_config_pwdebug': '1', 'npm_package_config_pwdebug': '0'},
            {'PWDEBUG': '1', 'npm_config_pwdebug': '0'},
        ):
            with self.subTest(overrides=overrides):
                env = os.environ.copy()
                for variable in DEBUG_ENV_VARS:
                    env.pop(variable, None)
                env.update(overrides)
                env['PASSWORD_TEST_SECRET'] = SYNTHETIC_PASSWORD
                result = subprocess.run(
                    ['node', str(CLI), 'test', '--config', str(CONFIG), '--list'],
                    cwd=UI_DIR,
                    env=env,
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertNotEqual(result.returncode, 0)
                output = result.stdout + result.stderr
                self.assertTrue(
                    'Playwright debug logging and inspector must be disabled' in output,
                    f'Guard message missing (exit {result.returncode}, output length {len(output)}).',
                )
                self.assertNotIn(SYNTHETIC_PASSWORD, output)

    def test_disabled_playwright_debugging_respects_precedence(self):
        for overrides in (
            {},
            {'PWDEBUG': '0', 'npm_config_pwdebug': '1'},
            {'PWDEBUG': '', 'npm_config_pwdebug': '1'},
            {'npm_config_pwdebug': '0', 'npm_package_config_pwdebug': '1'},
            {'npm_config_pwdebug': '', 'npm_package_config_pwdebug': '1'},
        ):
            with self.subTest(overrides=overrides):
                env = os.environ.copy()
                for variable in DEBUG_ENV_VARS:
                    env.pop(variable, None)
                env.update(overrides)
                result = subprocess.run(
                    ['node', str(CLI), 'test', '--config', str(CONFIG), '--list'],
                    cwd=UI_DIR,
                    env=env,
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == '__main__':
    unittest.main()
