import { defineConfig } from '@playwright/test';

import '../../../../libs/test-utils/src/general/suppress_sensitive_snapshots';

export default defineConfig({
  testDir: '.',
  testMatch: /password-report-fixture\.ts/,
  outputDir: process.env.PASSWORD_TEST_OUTPUT,
  expect: { timeout: 1_000 },
  use: { actionTimeout: 1_000, trace: 'off', video: 'off', screenshot: 'off' },
});
