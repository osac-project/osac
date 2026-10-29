import { type Page, expect } from '@playwright/test';

import { LoginPage } from '../../../locators/login_page';

export type LoginUserType = 'admin' | 'tenant_type';

const accountRoleByUserType: Record<LoginUserType, string> = {
  admin: 'Cloud provider admin',
  tenant_type: 'IdP manager',
};

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const isLoggedIn = async (page: Page): Promise<boolean> =>
  page
    .getByRole(...LoginPage.accountMenu)
    .isVisible()
    .catch(() => false);

const verifyLoggedInUser = async (
  page: Page,
  username: string,
  userType: LoginUserType,
): Promise<void> => {
  const accountMenu = page.getByRole(...LoginPage.accountMenu);
  await expect(accountMenu).toBeVisible();
  const accountMenuText = [username, accountRoleByUserType[userType]]
    .join(' ')
    .split(/\s+/)
    .map(escapeRegExp)
    .join('\\s+');
  await expect(accountMenu).toHaveText(new RegExp(`^\\s*${accountMenuText}\\s*$`));
  await expect(page.locator(...LoginPage.osacLogo)).toBeVisible();

  if (userType === 'tenant_type') {
    await expect(page.getByRole(...LoginPage.tenantIdentityProvidersPageTitle)).toBeVisible();
    await expect(page.getByRole(...LoginPage.createIdentityProvider)).toBeVisible();
  }
};

const isPasswordUpdateRequired = async (page: Page): Promise<boolean> =>
  page
    .getByRole(...LoginPage.passwordUpdatePageTitle)
    .isVisible()
    .catch(() => false);

/**
 * Open a link in the Playwright browser page and verify that the Keycloak
 * login page is displayed.
 */
export const openBrowser = async (page: Page, link: string): Promise<void> => {
  await page.goto(link);

  if (await isLoggedIn(page)) {
    return;
  }

  await expect(page.locator(...LoginPage.brand)).toContainText(/osac/i);
  await expect(page.getByRole(...LoginPage.pageTitle)).toBeVisible();
};

/** Log out the current browser session and clear its local session state. */
export const logout = async (page: Page): Promise<void> => {
  const response = await page.evaluate(async () => {
    const result = await fetch('/api/logout', { method: 'POST', credentials: 'include' });
    return { ok: result.ok, status: result.status };
  });
  if (!response.ok) {
    throw new Error(`Logout failed (HTTP ${response.status}).`);
  }

  await page.context().clearCookies();
  await page.evaluate(() => {
    localStorage.removeItem('osac.sessionExpiry');
    sessionStorage.clear();
  });
};

/**
 * Open the login page and authenticate with the supplied credentials.
 */
export const openAndLogin = async (
  page: Page,
  link: string,
  username: string,
  password: string,
  userType: LoginUserType,
  newPassword = '',
): Promise<void> => {
  if (!username || !password) {
    throw new Error(
      'OSAC_ADMIN_USERNAME and OSAC_ADMIN_PASSWORD must be set to a valid Keycloak test user.',
    );
  }

  await openBrowser(page, link);

  if (await isLoggedIn(page)) {
    await verifyLoggedInUser(page, username, userType);
    return;
  }

  await page.getByLabel(...LoginPage.username).fill(username);
  await page.getByRole(...LoginPage.signIn).click();

  // The first Keycloak may broker to a second Keycloak, which can present
  // another username screen before showing the password field.
  const usernameField = page.getByLabel(...LoginPage.username);
  const passwordField = page.locator(...LoginPage.password);
  await Promise.race([
    usernameField.waitFor({ state: 'visible' }).catch(() => {}),
    passwordField.waitFor({ state: 'visible' }).catch(() => {}),
  ]);
  if (await usernameField.isVisible().catch(() => false)) {
    await usernameField.fill(username);
    if (!(await passwordField.isVisible().catch(() => false))) {
      await page.getByRole(...LoginPage.signIn).click();
      await passwordField.waitFor({ state: 'visible' });
    }
  }

  await passwordField.fill(password);
  await page.getByRole(...LoginPage.signIn).click();

  const accountMenu = page.getByRole(...LoginPage.accountMenu);
  const passwordUpdatePage = page.getByRole(...LoginPage.passwordUpdatePageTitle);
  await expect(accountMenu.or(passwordUpdatePage)).toBeVisible();

  if (await isPasswordUpdateRequired(page)) {
    if (!newPassword) {
      throw new Error(
        'The authenticated user must change the password, but no new password was provided.',
      );
    }

    await page.getByLabel(...LoginPage.newPassword).fill(newPassword);
    await page.getByLabel(...LoginPage.confirmPassword).fill(newPassword);
    await page.getByRole(...LoginPage.passwordUpdateSubmit).click();
  }

  await verifyLoggedInUser(page, username, userType);
};
