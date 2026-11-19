import { type Locator, expect } from '@playwright/test';

export const fillPassword = async (field: Locator, password: string): Promise<void> => {
  // locator.fill records its value in Playwright steps and failure logs.
  // Keep the actionability checks, but pass the value through evaluate, whose
  // arguments are not included in the HTML report's step data.
  await expect(field).toBeVisible();
  await expect(field).toBeEditable();
  await field.focus();
  await field.evaluate((element, value) => {
    if (!(element instanceof HTMLInputElement) || element.type !== 'password') {
      throw new Error('Password field is not a password input.');
    }
    if (element.disabled || element.readOnly) {
      throw new Error('Password field is not editable.');
    }
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.bind(
      element,
    );
    if (!setter) {
      throw new Error('Password input setter is unavailable.');
    }
    setter(value);
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
  }, password);
};
