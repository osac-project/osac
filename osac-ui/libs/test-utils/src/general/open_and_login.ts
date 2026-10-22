import { type Page, expect } from '@playwright/test';

import { LoginPage } from '../../../locators/login_page';

const isLoggedIn = async (page: Page): Promise<boolean> =>
  page
    .getByRole(...LoginPage.accountMenu)
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

/**
 * Open the login page and authenticate with the supplied credentials.
 */
export const openAndLogin = async (
  page: Page,
  link: string,
  username: string,
  password: string,
): Promise<void> => {
  await openBrowser(page, link);

  if (await isLoggedIn(page)) {
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

  await expect(page.getByRole(...LoginPage.accountMenu)).toBeVisible();
  await expect(page.locator(...LoginPage.osacLogo)).toBeVisible();
};

/** Open the login page and authenticate with the configured test user. */
export const openAndLoginWithEnvironment = async (page: Page, link: string): Promise<void> => {
  const username = process.env.OSAC_USERNAME;
  const password = process.env.OSAC_PASSWORD;
  if (!username || !password) {
    throw new Error('OSAC_USERNAME and OSAC_PASSWORD must be set to a valid Keycloak test user.');
  }

  await openAndLogin(page, link, username, password);
};
