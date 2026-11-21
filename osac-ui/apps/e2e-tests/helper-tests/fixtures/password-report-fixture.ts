import { expect, test } from '@playwright/test';

import { fillPassword } from '../../../../libs/test-utils/src/general/fill_password';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';

const password = process.env.PASSWORD_TEST_SECRET ?? '';
const replacement = process.env.PASSWORD_TEST_REPLACEMENT ?? '';

test('password entry succeeds', async ({ page }) => {
  await page.setContent(
    '<input type="password"><script>window.inputEvents = 0; document.querySelector("input").addEventListener("input", () => window.inputEvents++)</script>',
  );
  const field = page.locator('input');
  await fillPassword(field, password);
  expect(
    await field.evaluate(
      (element, value) => (element as HTMLInputElement).value === value,
      password,
    ),
  ).toBe(true);
  expect(
    await page.evaluate(() => (window as unknown as { inputEvents: number }).inputEvents),
  ).toBe(1);
});

test('password entry fails when the field is disabled', async ({ page }) => {
  await page.setContent('<input type="password" disabled>');
  await fillPassword(page.locator('input'), password);
});

test('a later test failure does not expose the entered password', async ({ page }) => {
  await page.setContent('<input type="password">');
  await fillPassword(page.locator('input'), password);
  throw new Error('synthetic failure after password entry');
});

test('password entry error does not expose the value', async ({ page }) => {
  await page.setContent('<input type="password">');
  await page.evaluate(() => {
    Object.defineProperty(HTMLInputElement.prototype, 'value', {
      set: () => {
        throw new Error('synthetic password input error');
      },
    });
  });
  await fillPassword(page.locator('input'), password);
});

test('fails if the password field becomes disabled while focusing', async ({ page }) => {
  await page.setContent('<input type="password" onfocus="this.disabled = true">');
  await fillPassword(page.locator('input'), password);
});

for (const requireUpdate of [false, true]) {
  test(`GUI login ${requireUpdate ? 'with' : 'without'} password update`, async ({ page }) => {
    await page.route('http://login.test/', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: `<div id="kc-header-wrapper">OSAC</div><h1>Sign in to your account</h1>
          <label>Username or email<input id="username"></label>
          <input id="password" type="password" hidden>
          <button id="sign-in">Sign In</button><div id="update" hidden>
            <h1>Update password</h1><label>New Password<input type="text" id="new"></label>
            <label>Confirm password<input type="text" id="confirm"></label>
            <button id="submit">Submit</button></div><div id="account" hidden>
            <button aria-label="Account menu">admin Cloud provider admin</button>
            <div class="pf-v6-c-masthead__brand"><a aria-label="Red Hat">OSAC</a></div>
          </div><script>
            const signIn = document.getElementById('sign-in');
            const passwordField = document.getElementById('password');
            signIn.onclick = () => {
              if (passwordField.hidden) { passwordField.hidden = false; return; }
              document.querySelector('label').hidden = true;
              passwordField.hidden = true; passwordField.type = 'hidden'; signIn.hidden = true;
              document.getElementById('new').type = 'password';
              document.getElementById('confirm').type = 'password';
              document.getElementById(${JSON.stringify(requireUpdate ? 'update' : 'account')}).hidden = false;
            };
            document.getElementById('submit').onclick = () => {
              document.getElementById('update').hidden = true;
              document.getElementById('account').hidden = false;
            };
          </script>`,
      }),
    );
    const updated = await openAndLogin(
      page,
      'http://login.test/',
      'admin',
      password,
      'admin',
      replacement,
    );
    expect(updated).toBe(requireUpdate);
    expect(
      await page
        .locator('#password')
        .evaluate((element, value) => (element as HTMLInputElement).value === value, password),
    ).toBe(true);
    if (requireUpdate) {
      expect(
        await page
          .locator('#new')
          .evaluate((element, value) => (element as HTMLInputElement).value === value, replacement),
      ).toBe(true);
      expect(
        await page
          .locator('#confirm')
          .evaluate((element, value) => (element as HTMLInputElement).value === value, replacement),
      ).toBe(true);
    }
  });
}
