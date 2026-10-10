import { test as setup } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

import { AUTH_FILE } from './auth-file';
import { openAndLogin } from '../../../libs/test-utils/src/general/open_and_login';

setup('authenticate', async ({ page }) => {
  const username = process.env.OSAC_USERNAME;
  const password = process.env.OSAC_PASSWORD;
  if (!username || !password) {
    throw new Error('OSAC_USERNAME and OSAC_PASSWORD must be set to a valid Keycloak test user.');
  }

  await openAndLogin(page, '/', username, password);

  // Harden the .auth directory before storageState writes into it — AUTH_FILE
  // holds a live, real Keycloak session cookie, and creating the dir with
  // default permissions first would briefly expose it before the chmod below.
  fs.mkdirSync(path.dirname(AUTH_FILE), { recursive: true, mode: 0o700 });
  await page.context().storageState({ path: AUTH_FILE });
  // Restrict to the current user so other local accounts on a shared machine
  // can't reuse the session; also re-assert dir perms in case it pre-existed
  // with looser permissions.
  fs.chmodSync(path.dirname(AUTH_FILE), 0o700);
  fs.chmodSync(AUTH_FILE, 0o600);
});
