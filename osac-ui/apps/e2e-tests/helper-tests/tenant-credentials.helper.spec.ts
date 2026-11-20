import { expect, test } from '@playwright/test';
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

import {
  loginWithStagedTenantPassword,
  removeTenantCredentials,
  tenantCredentialsFilePath,
  tenantReplacementPassword,
} from '../../../libs/test-utils/src/tenants.page';
import { openAndLogin } from '../../../libs/test-utils/src/general/open_and_login';

test('generates distinct replacement passwords when none is configured', () => {
  const first = tenantReplacementPassword();
  expect(first).toMatch(/^Aa1![A-Za-z0-9_-]{32}$/);
  expect(tenantReplacementPassword()).not.toBe(first);
  expect(tenantReplacementPassword(' configured-password ')).toBe('configured-password');
});

test('stages the replacement before login and promotes it after success', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Tenant: tenant-test\nUsername: test-user\nPassword: old\n');
    const replacement = tenantReplacementPassword();
    await loginWithStagedTenantPassword(file, replacement, async () => {
      expect(await readFile(file, 'utf8')).toContain('Password: old\n');
      expect(await readFile(`${file}.pending`, 'utf8')).toContain(`Password: ${replacement}\n`);
      expect((await stat(`${file}.pending`)).mode & 0o777).toBe(0o600);
      return true;
    });
    const contents = await readFile(file, 'utf8');
    expect(contents.includes(`Password: ${replacement}\n`)).toBe(true);
    await expect(stat(`${file}.pending`)).rejects.toThrow();
    await expect(loginWithStagedTenantPassword(file, '', async () => true)).rejects.toThrow(
      'Tenant replacement password is empty.',
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('retains both credentials when login fails after password submission', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Username: test-user\nPassword: old\n');
    const replacement = tenantReplacementPassword();
    await expect(
      loginWithStagedTenantPassword(file, replacement, async () => {
        throw new Error('Login verification failed');
      }),
    ).rejects.toThrow('Login verification failed');
    expect(await readFile(file, 'utf8')).toContain('Password: old\n');
    expect(await readFile(`${file}.pending`, 'utf8')).toContain(`Password: ${replacement}\n`);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('retains staged credentials when verification fails after the password form', async ({
  page,
}) => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Username: admin\nPassword: old\n');
    const replacement = tenantReplacementPassword();
    await page.route('http://login.test/', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: `<div id="kc-header-wrapper">OSAC</div><h1>Sign in to your account</h1>
          <label>Username or email<input id="username"></label>
          <input id="password" type="password" hidden><button id="sign-in">Sign In</button>
          <div id="update" hidden><h1>Update password</h1>
            <label>New Password<input type="text" id="new"></label>
            <label>Confirm password<input type="text" id="confirm"></label>
            <button id="submit">Submit</button></div>
          <button id="account" aria-label="Account menu" hidden>wrong role</button>
          <script>
            const signIn = document.getElementById('sign-in');
            const password = document.getElementById('password');
            signIn.onclick = () => {
              if (password.hidden) { password.hidden = false; return; }
              document.querySelector('label').hidden = true;
              password.hidden = true; password.type = 'hidden'; signIn.hidden = true;
              document.getElementById('new').type = 'password';
              document.getElementById('confirm').type = 'password';
              document.getElementById('update').hidden = false;
            };
            document.getElementById('submit').onclick = () => {
              document.getElementById('update').hidden = true;
              document.getElementById('account').hidden = false;
            };
          </script>`,
      }),
    );
    await expect(
      loginWithStagedTenantPassword(file, replacement, () =>
        openAndLogin(page, 'http://login.test/', 'admin', 'old', 'admin', replacement),
      ),
    ).rejects.toThrow();
    await expect(page.locator('#account')).toBeVisible();
    expect(await readFile(file, 'utf8')).toContain('Password: old\n');
    expect(await readFile(`${file}.pending`, 'utf8')).toContain(`Password: ${replacement}\n`);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('does not submit a password when staging fails', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Username: test-user\nPassword: old\n');
    await writeFile(`${file}.pending`, 'existing recovery data', { mode: 0o600 });
    let submitted = false;
    await expect(
      loginWithStagedTenantPassword(file, tenantReplacementPassword(), async () => {
        submitted = true;
        return true;
      }),
    ).rejects.toThrow('Could not stage the tenant replacement credentials.');
    expect(submitted).toBe(false);
    expect(await readFile(`${file}.pending`, 'utf8')).toBe('existing recovery data');
    expect(await readFile(file, 'utf8')).toContain('Password: old\n');
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('retains staged credentials when promotion fails', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Username: test-user\nPassword: old\n');
    const replacement = tenantReplacementPassword();
    await expect(
      loginWithStagedTenantPassword(file, replacement, async () => {
        await rm(file);
        await mkdir(file);
        return true;
      }),
    ).rejects.toThrow('Could not finish updating the tenant credentials.');
    expect(await readFile(`${file}.pending`, 'utf8')).toContain(`Password: ${replacement}\n`);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('discards staged credentials when no password change was needed', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  try {
    const file = join(directory, 'credentials');
    await writeFile(file, 'Username: test-user\nPassword: old\n');
    await loginWithStagedTenantPassword(file, tenantReplacementPassword(), async () => false);
    expect(await readFile(file, 'utf8')).toContain('Password: old\n');
    await expect(stat(`${file}.pending`)).rejects.toThrow();
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('uses the configured private directory and removes only the deleted tenant credentials', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'osac-tenant-password-test-'));
  const previousDirectory = process.env.OSAC_E2E_CREDENTIALS_DIR;
  try {
    process.env.OSAC_E2E_CREDENTIALS_DIR = directory;
    const file = tenantCredentialsFilePath('tenant-test');
    const otherFile = tenantCredentialsFilePath('other-tenant');
    await mkdir(dirname(file), { mode: 0o700 });
    await mkdir(dirname(otherFile), { mode: 0o700 });
    await writeFile(file, 'test');
    await writeFile(`${file}.pending`, 'test');
    await writeFile(otherFile, 'other');
    await removeTenantCredentials('tenant-test');
    await expect(stat(file)).rejects.toThrow();
    await expect(stat(`${file}.pending`)).rejects.toThrow();
    expect(await readFile(otherFile, 'utf8')).toBe('other');
  } finally {
    if (previousDirectory === undefined) {
      delete process.env.OSAC_E2E_CREDENTIALS_DIR;
    } else {
      process.env.OSAC_E2E_CREDENTIALS_DIR = previousDirectory;
    }
    await rm(directory, { recursive: true, force: true });
  }
});
