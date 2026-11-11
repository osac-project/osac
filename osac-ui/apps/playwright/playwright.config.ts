import { defineConfig, devices } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { AUTH_FILE } from './src/auth-file';
import {
  readVmaasFullFlowRunState,
  writeVmaasFullFlowRunState,
} from '../e2e-tests/src/vmaas/run_state';

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

const flowMode = process.env.OSAC_VM_FLOW_MODE ?? 'full';
if (flowMode !== 'create' && flowMode !== 'delete' && flowMode !== 'full') {
  throw new Error('OSAC_VM_FLOW_MODE must be create, delete, or full.');
}

const hasExplicitRunState =
  Boolean(process.env.OSAC_CATALOG_TEST_RUN_ID) &&
  Boolean(process.env.OSAC_DISK_IMAGE_TEST_RUN_ID) &&
  Boolean(process.env.OSAC_STORAGE_TIER_TEST_RUN_ID);
const persistedRunState =
  flowMode === 'delete' && !hasExplicitRunState ? readVmaasFullFlowRunState() : undefined;

// Share one unique ID across Playwright projects so deletion only targets this run's image.
const diskImageTestRunId =
  process.env.OSAC_DISK_IMAGE_TEST_RUN_ID ??
  persistedRunState?.diskImageTestRunId ??
  randomUUID().replace(/-/g, '').slice(0, 12);
if (!/^[a-z0-9]{1,32}$/i.test(diskImageTestRunId)) {
  throw new Error('OSAC_DISK_IMAGE_TEST_RUN_ID must contain 1-32 letters or digits.');
}
process.env.OSAC_DISK_IMAGE_TEST_RUN_ID = diskImageTestRunId.toLowerCase();

// Share a unique ID across Storage tier projects so teardown only targets this run's tier.
const storageTierTestRunId =
  process.env.OSAC_STORAGE_TIER_TEST_RUN_ID ??
  persistedRunState?.storageTierTestRunId ??
  randomUUID().replace(/-/g, '').slice(0, 12);
if (!/^[a-z0-9]{1,32}$/i.test(storageTierTestRunId)) {
  throw new Error('OSAC_STORAGE_TIER_TEST_RUN_ID must contain 1-32 letters or digits.');
}
process.env.OSAC_STORAGE_TIER_TEST_RUN_ID = storageTierTestRunId.toLowerCase();

// Share a unique ID across catalog projects so teardown only targets this run's item.
const catalogTestRunId =
  process.env.OSAC_CATALOG_TEST_RUN_ID ??
  persistedRunState?.catalogTestRunId ??
  randomUUID().replace(/-/g, '').slice(0, 12);
if (!/^[a-z0-9]{1,32}$/i.test(catalogTestRunId)) {
  throw new Error('OSAC_CATALOG_TEST_RUN_ID must contain 1-32 letters or digits.');
}
process.env.OSAC_CATALOG_TEST_RUN_ID = catalogTestRunId.toLowerCase();

const fullFlowProjectRequested = process.argv.some(
  (argument) =>
    argument.includes('vmaas-full-flow') || argument.includes('vmaas-full-happy-basic-flow'),
);
if (flowMode !== 'delete' && fullFlowProjectRequested && !process.argv.includes('--list')) {
  writeVmaasFullFlowRunState({
    catalogTestRunId: catalogTestRunId.toLowerCase(),
    diskImageTestRunId: diskImageTestRunId.toLowerCase(),
    storageTierTestRunId: storageTierTestRunId.toLowerCase(),
  });
}

// Ad hoc specs (typically written by an AI agent to manually verify a change)
// live in this gitignored scratch directory, never under src/ — that keeps
// the "throwaway, not a test suite" boundary obvious to anyone browsing the
// package, instead of relying on everyone remembering src/*.spec.ts is ignored.
const scratchDir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'scratch');
fs.mkdirSync(scratchDir, { recursive: true });
const vmaasDir = path.join(path.dirname(fileURLToPath(import.meta.url)), '../e2e-tests/src/vmaas');
const preResourceBaseCreateTestMatch =
  /create\.(instance_type|disk_images|storage_tier|tenant)\.spec\.ts/;
const preResourceCatalogCreateTestMatch = /create\.catalog\.spec\.ts/;
const preResourceDeleteTestMatch =
  /delete\.(catalog|disk_images|instance_type|storage_tier|tenant)\.spec\.ts/;
const preResourceTestMatch =
  flowMode === 'delete' ? preResourceDeleteTestMatch : preResourceCatalogCreateTestMatch;
const diskImageTestMatch =
  flowMode === 'delete' ? /delete\.disk_images\.spec\.ts/ : /create\.disk_images\.spec\.ts/;
const instanceTypeTestMatch =
  flowMode === 'delete' ? /delete\.instance_type\.spec\.ts/ : /create\.instance_type\.spec\.ts/;
const storageTierTestMatch =
  flowMode === 'delete' ? /delete\.storage_tier\.spec\.ts/ : /create\.storage_tier\.spec\.ts/;
const tenantTestMatch =
  flowMode === 'delete' ? /delete\.tenant\.spec\.ts/ : /create\.tenant\.spec\.ts/;
const catalogTestMatch =
  flowMode === 'delete' ? /delete\.catalog\.spec\.ts/ : /create\.catalog\.spec\.ts/;

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
  // These tests mutate shared live resources. Stop after the first failure so
  // duplicate-resource failures do not allow the rest of the flow to proceed.
  maxFailures: 1,
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
      testMatch: tenantTestMatch,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      teardown: flowMode === 'full' ? 'vmaas-delete' : undefined,
    },
    {
      name: 'vmaas-full-flow-pre-resource-base',
      testDir: vmaasDir,
      testMatch: preResourceBaseCreateTestMatch,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-full-flow-pre-resource',
      testDir: vmaasDir,
      testMatch: preResourceTestMatch,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: flowMode === 'delete' ? [] : ['vmaas-full-flow-pre-resource-base'],
      teardown: flowMode === 'full' ? 'vmaas-full-flow-pre-resource-delete' : undefined,
    },
    {
      name: 'vmaas-full-flow',
      testDir: vmaasDir,
      testMatch: /create\.vm\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: ['vmaas-full-flow-pre-resource'],
    },
    {
      name: 'vmaas-full-flow-vm-delete',
      testDir: vmaasDir,
      testMatch: /delete\.vm\.spec\.ts/,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      // In delete mode this is the first cleanup project. In full mode it
      // follows the complete creation chain before deleting the VM.
      dependencies: flowMode === 'full' ? ['vmaas-full-flow'] : [],
    },
    {
      name: 'vmaas-full-happy-basic-flow',
      testDir: vmaasDir,
      testMatch: /delete\.vm\.spec\.ts/,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: flowMode === 'full' ? ['vmaas-full-flow'] : [],
    },
    {
      name: 'vmaas-full-flow-pre-resource-delete',
      testDir: vmaasDir,
      testMatch: preResourceDeleteTestMatch,
      fullyParallel: false,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-storage-tier',
      testDir: vmaasDir,
      testMatch: storageTierTestMatch,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      teardown: flowMode === 'full' ? 'vmaas-storage-tier-delete' : undefined,
    },
    {
      name: 'vmaas-storage-tier-delete',
      testDir: vmaasDir,
      testMatch: /delete\.storage_tier\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-instance-type',
      testDir: vmaasDir,
      testMatch: instanceTypeTestMatch,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      teardown: flowMode === 'full' ? 'vmaas-instance-type-delete' : undefined,
    },
    {
      name: 'vmaas-instance-type-lifecycle',
      testDir: vmaasDir,
      testMatch: /lifecycle\.instance_type\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: ['vmaas-instance-type'],
    },
    {
      name: 'vmaas-instance-type-delete',
      testDir: vmaasDir,
      testMatch: /delete\.instance_type\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-delete',
      testDir: vmaasDir,
      testMatch: /delete\.tenant\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-disk-image',
      testDir: vmaasDir,
      testMatch: diskImageTestMatch,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      teardown: flowMode === 'full' ? 'vmaas-disk-image-delete' : undefined,
    },
    {
      name: 'vmaas-disk-image-lifecycle',
      testDir: vmaasDir,
      testMatch: /lifecycle\.disk_images\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: ['vmaas-disk-image'],
    },
    {
      name: 'vmaas-disk-image-delete',
      testDir: vmaasDir,
      testMatch: /delete\.disk_images\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
    {
      name: 'vmaas-catalog',
      testDir: vmaasDir,
      testMatch: catalogTestMatch,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
      dependencies: flowMode === 'delete' ? [] : ['vmaas-disk-image', 'vmaas-instance-type'],
      teardown: flowMode === 'full' ? 'vmaas-catalog-delete' : undefined,
    },
    {
      name: 'vmaas-catalog-delete',
      testDir: vmaasDir,
      testMatch: /delete\.catalog\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
    },
  ],
});
