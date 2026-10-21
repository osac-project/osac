import { type Locator, expect } from '@playwright/test';

/** Locate a table row by the exact text of one of its cells. */
export const getTableRowByCellText = (
  table: Locator,
  rowLocator: readonly [selector: string],
  cellLocator: readonly [selector: string],
  cellText: RegExp,
): Locator =>
  table
    .locator(...rowLocator)
    .locator(...cellLocator)
    .filter({ hasText: cellText })
    .locator('..');

/** Verify that a resource row is absent and report a clear duplicate error otherwise. */
export const expectRowNotToExist = async (
  row: Locator,
  duplicateMessage: string,
): Promise<void> => {
  try {
    await expect(row).toHaveCount(1, { timeout: 3_000 });
  } catch {
    if ((await row.count()) === 0) {
      return;
    }
  }

  throw new Error(duplicateMessage);
};

/** Confirm a deletion and wait for the resource row to disappear. */
export const confirmRowDeletion = async (
  row: Locator,
  deleteDialog: Locator,
  deleteTitle: Locator,
  deleteWarning: Locator,
  deleteButton: Locator,
  resourceName: string,
  resourceLabel: string,
  timeout: number,
  statusCell?: Locator,
): Promise<void> => {
  await expect(deleteDialog).toBeVisible();
  await expect(deleteTitle).toContainText(`Delete ${resourceName}?`);
  await expect(deleteWarning).toBeVisible();
  await deleteButton.click();
  await expect(deleteDialog).toBeHidden();

  try {
    await expect(row).toHaveCount(0, { timeout });
  } catch {
    const remainingRows = await row.count();
    const currentStatus =
      remainingRows === 1 && statusCell
        ? ((await statusCell.textContent())?.trim() ?? 'unknown')
        : 'unknown';

    throw new Error(
      `${resourceLabel} "${resourceName}" was not removed from the table within ` +
        `${timeout / 1_000} seconds after deletion. The row is still present with status ` +
        `"${currentStatus}".`,
    );
  }
};
