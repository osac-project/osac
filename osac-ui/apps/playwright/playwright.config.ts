import { defineConfig, devices } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { AUTH_FILE } from './src/auth-file';

const baseURL = process.env.OSAC_UI_BASE_URL;
if (!baseURL) {
  throw new Error('OSAC_UI_BASE_URL must be set to the URL of a running osac-ui instance.');
}
// Diagnostics below never interpolate the raw value — a malformed OSAC_UI_BASE_URL
// can carry embedded credentials or an internal hostname that shouldn't land
// in CI logs.
const invalidBaseURLError = new Error(
  'OSAC_UI_BASE_URL must be an absolute http(s) URL with no embedded username/password.',
);
let parsedBaseURL: URL;
try {
  parsedBaseURL = new URL(baseURL);
} catch {
  throw invalidBaseURLError;
}
if (parsedBaseURL.protocol !== 'http:' && parsedBaseURL.protocol !== 'https:') {
  throw invalidBaseURLError;
}
if (parsedBaseURL.username || parsedBaseURL.password) {
  throw invalidBaseURLError;
}

// Ad hoc specs (typically written by an AI agent to manually verify a change)
// live in this gitignored scratch directory, never under src/ — that keeps
// the "throwaway, not a test suite" boundary obvious to anyone browsing the
// package, instead of relying on everyone remembering src/*.spec.ts is ignored.
const scratchDir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'scratch');
fs.mkdirSync(scratchDir, { recursive: true });
const vmaasDir = path.join(path.dirname(fileURLToPath(import.meta.url)), '../e2e-tests/vmaas');

// Video recording is opt-in via PW_VIDEO=on (default off) — handy for debugging
// a flow by watching the recording afterwards. PW_SLOW_MO adds a per-action
// delay in ms (e.g. PW_SLOW_MO=500) so the recording plays at human speed
// instead of instant fills/clicks. Note: enabling video also records the setup
// project, which types the real Keycloak password — leave PW_VIDEO off unless
// you're deliberately debugging the login flow.
const video = process.env.PW_VIDEO === 'on' ? 'on' : 'off';
const slowMo = Number(process.env.PW_SLOW_MO) || 0;

export default defineConfig({
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'html',
  expect: {
    timeout: 15_000,
  },
  use: {
    baseURL,
    trace: 'on-first-retry',
    video,
    launchOptions: { slowMo },
    // Dev/lab cluster routes commonly present a self-signed or cluster-internal
    // CA cert that Chromium doesn't trust by default (the same reason manual
    // testing against these environments always needs curl -k). Opt-in only —
    // this flow submits a real Keycloak password, so TLS verification stays on
    // by default.
    ignoreHTTPSErrors: process.env.IGNORE_HTTPS_ERRORS === 'true',
  },
  projects: [
    {
      name: 'setup',
      testDir: './src',
      testMatch: /auth\.setup\.ts/,
      // Traces record fill() arguments — never trace the project that types the
      // real Keycloak password, even if a future CI run retries it.
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'smoke',
      testDir: './src',
      testMatch: /smoke\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], storageState: AUTH_FILE },
      dependencies: ['setup'],
    },
    {
      name: 'chromium',
      testDir: scratchDir,
      use: { ...devices['Desktop Chrome'], storageState: AUTH_FILE },
      dependencies: ['setup'],
    },
    {
      name: 'vmaas',
      testDir: vmaasDir,
      testMatch: /.*\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
  ],
});
