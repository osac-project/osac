import { type Locator, expect } from '@playwright/test';

interface ExpectRowRemovedParams {
  row: Locator;
  statusCell: Locator;
  resource: string;
  table: string;
  timeout?: number;
}

/** Wait for a resource row to disappear and report its status if it remains. */
export const expectRowRemoved = async ({
  row,
  statusCell,
  resource,
  table,
  timeout = 120_000,
}: ExpectRowRemovedParams): Promise<void> => {
  try {
    await expect(row).toHaveCount(0, { timeout });
  } catch {
    const remainingRows = await row.count();
    const currentStatus =
      remainingRows === 1 ? ((await statusCell.textContent())?.trim() ?? 'unknown') : 'unknown';
    throw new Error(
      `${resource} was not removed from the ${table} table within ` +
        `${Math.round(timeout / 1000)} seconds after deletion. ` +
        `The row is still present with status "${currentStatus}".`,
    );
  }
};
