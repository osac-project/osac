import { expect, test } from '@playwright/test';

import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';

test('logs into the OSAC UI', async ({ page }) => {
  const username = process.env.OSAC_USERNAME;
  const password = process.env.OSAC_PASSWORD;
  if (!username || !password) {
    throw new Error('OSAC_USERNAME and OSAC_PASSWORD must be set to a valid test user.');
  }

  await openAndLogin(page, '/', username, password);

  await expect(page.getByRole('button', { name: 'Account menu' })).toBeVisible();
});
