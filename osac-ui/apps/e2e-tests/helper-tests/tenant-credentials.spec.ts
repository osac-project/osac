import { expect, test } from '@playwright/test';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import {
  tenantReplacementPassword,
  updateTenantCredentialsPassword,
} from '../../../libs/test-utils/src/tenants.page';

test('generates distinct replacement passwords when none is configured', () => {
  const first = tenantReplacementPassword();
  expect(first).toMatch(/^Aa1![A-Za-z0-9_-]{32}$/);
  expect(tenantReplacementPassword()).not.toBe(first);
  expect(tenantReplacementPassword(' configured-password ')).toBe('configured-password');
});

test('persists the chosen replacement password for later tenant login', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Tenant: tenant-test\nUsername: test-user\nPassword: old\n');
    const replacement = tenantReplacementPassword();
    await updateTenantCredentialsPassword(file, replacement);
    const contents = await readFile(file, 'utf8');
    expect(contents.includes(`Password: ${replacement}\n`)).toBe(true);
    await expect(updateTenantCredentialsPassword(file, '')).rejects.toThrow(
      'Tenant replacement password is empty.',
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
