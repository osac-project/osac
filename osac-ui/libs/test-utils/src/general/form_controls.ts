import { type Page, expect } from '@playwright/test';

export type TypeaheadField = readonly [
  role: 'button',
  options: { readonly name: string; readonly exact: true },
];

/** Select an option from a PatternFly typeahead field. */
export const selectOption = async (
  page: Page,
  field: TypeaheadField,
  optionName: string | RegExp,
  index = 0,
): Promise<string> => {
  const fieldLocator = page.getByRole(...field).nth(index);
  await expect(fieldLocator).toBeVisible();
  await fieldLocator.click();
  const option = page.getByRole('option', { name: optionName }).first();
  await expect(option).toBeVisible();
  const selectedName = (await option.innerText()).trim();
  await option.click();
  return selectedName;
};
