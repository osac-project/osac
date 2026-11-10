import { type Page, expect } from '@playwright/test';

export type TypeaheadField =
  | readonly [role: 'button', options: { readonly name: string; readonly exact: true }]
  | readonly [selector: string];

/** Select an option from a PatternFly typeahead field. */
export const selectOption = async (
  page: Page,
  field: TypeaheadField,
  optionName: string | RegExp,
  index = 0,
  exact = false,
): Promise<string> => {
  const fieldLocator =
    field.length === 1
      ? page.locator(field[0]).nth(index)
      : page.getByRole(field[0], field[1]).nth(index);
  await expect(fieldLocator).toBeVisible();
  await fieldLocator.click();
  const option = page.getByRole('option', { name: optionName, exact }).first();
  await expect(option).toBeVisible();
  const selectedName = (await option.innerText()).trim();
  await option.click();
  return selectedName;
};
