import type { Page } from '@playwright/test';

export const LoginPage = {
  brand: (page: Page) => page.locator('#kc-header-wrapper'),
  pageTitle: (page: Page) => page.getByRole('heading', { name: 'Sign in to your account' }),
  username: (page: Page) => page.getByLabel('Username or email'),
  password: (page: Page) => page.locator('input[type="password"]'),
  signIn: (page: Page) => page.getByRole('button', { name: 'Sign In' }),
  accountMenu: (page: Page) => page.getByRole('button', { name: 'Account menu' }),
  osacLogo: (page: Page) => page.locator('svg.injected-svg[data-src*="RH-OSAC"]'),
};
