import { type Page, expect } from '@playwright/test';

import { expectRowNotToExist } from './general/row_assertions';
import type { LifecycleAction } from './lifecycle';
import {
  DiskImageCreatePage,
  DiskImageDetailPage,
  DiskImagesMainPage,
} from '../../locators/disk_images';

export interface DiskImageCreateParams {
  name: string;
  sourceType: 'Registry';
  sourceRef: string;
  guestOsFamily: string;
  architectures: readonly [string, ...string[]];
  scope: string;
}

export type DiskImageLifecycleState = 'Available' | 'Deprecated' | 'Obsolete';

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// Match the definition paired with an exact <dt> label so an unrelated value
// elsewhere on the page cannot satisfy the field assertion.
const diskImageFieldValue = (page: Page, field: string) =>
  page
    .locator('dt')
    .filter({ hasText: new RegExp(`^\\s*${escapeRegExp(field)}\\s*$`) })
    .locator('xpath=following-sibling::dd[1]');

// PatternFly can expose line-wrapped spaces in a truncated name cell's accessible name.
const diskImageLink = (page: Page, diskImageName: string) => {
  const accessibleName = new RegExp(`^${Array.from(diskImageName, escapeRegExp).join('\\s*')}$`);
  return page
    .getByRole(...DiskImagesMainPage.diskImagesTable)
    .getByRole('link', { name: accessibleName });
};

/** Verify a disk image detail value in the description-list row for its label. */
export const expectDiskImageField = async (
  page: Page,
  field: string,
  expectedValue: string,
): Promise<void> => {
  await expect(diskImageFieldValue(page, field)).toHaveText(expectedValue);
};

/** Verify the submitted values in their labeled rows and the initial Available lifecycle. */
export const expectDiskImageDetails = async (
  page: Page,
  diskImage: DiskImageCreateParams,
): Promise<void> => {
  await expect(page.getByRole('heading', { name: diskImage.name, exact: true })).toBeVisible();
  await expectDiskImageField(page, 'Source type', diskImage.sourceType);
  await expectDiskImageField(page, 'Source reference', diskImage.sourceRef);
  await expectDiskImageField(page, 'Guest OS family', diskImage.guestOsFamily);
  await expectDiskImageField(page, 'Architecture', diskImage.architectures.join(', '));
  await expectDiskImageLifecycle(page, 'Available');
  await expectDiskImageField(page, 'Scope', diskImage.scope);
};

/** Verify lifecycle text. Button-state assertions are temporarily disabled for OSAC-6385. */
export const expectDiskImageLifecycle = async (
  page: Page,
  lifecycle: DiskImageLifecycleState,
): Promise<void> => {
  await expect(diskImageFieldValue(page, 'Lifecycle')).toHaveText(lifecycle);

  // TODO(OSAC-6385): Restore these button assertions once disk image lifecycle actions
  // match the instance type behavior: Available shows Deprecate/Obsolete, Deprecated
  // shows Obsolete/Reactivate, and Obsolete shows Deprecate/Reactivate/Delete.
  /*
  const diskImageLifecycleActions: Record<
    DiskImageLifecycleState,
    { visible: readonly string[]; hidden: readonly string[] }
  > = {
    Available: {
      visible: ['Deprecate', 'Obsolete'],
      hidden: ['Reactivate', 'Delete'],
    },
    Deprecated: {
      visible: ['Obsolete', 'Reactivate'],
      hidden: ['Deprecate', 'Delete'],
    },
    Obsolete: {
      visible: ['Deprecate', 'Reactivate', 'Delete'],
      hidden: ['Obsolete'],
    },
  };

  for (const action of diskImageLifecycleActions[lifecycle].visible) {
    await expect(page.getByRole('button', { name: action, exact: true })).toBeVisible();
  }

  for (const action of diskImageLifecycleActions[lifecycle].hidden) {
    await expect(page.getByRole('button', { name: action, exact: true })).toBeHidden();
  }
  */
};

/** Click a lifecycle action and verify the resulting lifecycle text. */
export const transitionDiskImageLifecycle = async (
  page: Page,
  action: LifecycleAction,
  expectedLifecycle: DiskImageLifecycleState,
): Promise<void> => {
  await page.getByRole('button', { name: action, exact: true }).click();
  await expectDiskImageLifecycle(page, expectedLifecycle);
};

/** Open the disk image creation page and verify its title and required fields. */
export const openCreateDiskImagePage = async (page: Page): Promise<void> => {
  const createDiskImage = page.getByRole(...DiskImagesMainPage.createDiskImage);
  await expect(createDiskImage).toBeVisible();
  await createDiskImage.click();

  await expect(page.getByRole(...DiskImageCreatePage.pageTitle)).toBeVisible();
  await expect(page.locator(...DiskImageCreatePage.name)).toBeVisible();
  await expect(page.locator(...DiskImageCreatePage.sourceReference)).toBeVisible();
};

/** Fill and submit a Linux AMD64 disk image with Global scope. */
export const createDiskImage = async (
  page: Page,
  diskImage: DiskImageCreateParams,
): Promise<void> => {
  await page.locator(...DiskImageCreatePage.name).fill(diskImage.name);
  await page.locator(...DiskImageCreatePage.sourceReference).fill(diskImage.sourceRef);

  // Guest OS family and scope are single-select menus.
  await page.locator(...DiskImageCreatePage.guestOsFamily).click();
  await page.getByRole('option', { name: diskImage.guestOsFamily, exact: true }).click();

  await page.locator(...DiskImageCreatePage.scope).click();
  await page.getByRole('option', { name: diskImage.scope, exact: true }).click();

  // Architecture is a multi-select typeahead, so select each supplied value.
  const architectureToggle = page.locator(...DiskImageCreatePage.architecture);
  for (const architecture of diskImage.architectures) {
    if ((await architectureToggle.getAttribute('aria-expanded')) !== 'true') {
      await architectureToggle.click();
    }
    await page.getByRole('option', { name: architecture, exact: true }).click();
  }

  // The form fixes source type to Registry; the detail page verifies its value.
  await page.getByRole(...DiskImageCreatePage.create).click();

  await expectDiskImageDetails(page, diskImage);
};

/** Return to the Disk images list and verify the created image appears in its table. */
export const expectDiskImageListed = async (page: Page, diskImageName: string): Promise<void> => {
  await page
    .getByRole(...DiskImageDetailPage.breadcrumb)
    .getByRole(...DiskImageDetailPage.diskImagesBreadcrumb)
    .click();

  await expect(page.getByRole(...DiskImagesMainPage.pageTitle)).toBeVisible();
  const diskImagesTable = page.getByRole(...DiskImagesMainPage.diskImagesTable);
  await expect(diskImagesTable).toBeVisible();
  await expect(diskImageLink(page, diskImageName)).toBeVisible();
};

/** Open a disk image detail page from its exact table link. */
export const openDiskImageDetails = async (page: Page, diskImageName: string): Promise<void> => {
  const link = diskImageLink(page, diskImageName);
  await expect(link).toBeVisible();
  await link.click();
  await expect(page.getByRole('heading', { name: diskImageName, exact: true })).toBeVisible();
};

/** Include obsolete images in the disk image list. */
export const includeObsoleteDiskImages = async (page: Page): Promise<void> => {
  const listUrl = new URL(page.url());
  listUrl.searchParams.set('showObsolete', 'true');
  await page.goto(listUrl.toString());

  const showObsolete = page.locator(...DiskImagesMainPage.showObsolete);
  await expect(showObsolete).toBeChecked();
};

/** Verify that no disk image with this name exists, including obsolete images. */
export const expectDiskImageNotToExist = async (
  page: Page,
  diskImageName: string,
): Promise<void> => {
  await includeObsoleteDiskImages(page);
  await page.getByRole('textbox', { name: 'Search disk images', exact: true }).fill(diskImageName);
  await expectRowNotToExist(
    diskImageLink(page, diskImageName),
    `Cannot create disk image "${diskImageName}": a disk image with this name already exists.`,
  );
};

/** Confirm deletion and verify the disk image is absent, including obsolete rows. */
export const deleteDiskImage = async (page: Page, diskImageName: string): Promise<void> => {
  const obsoleteAction = page.getByRole('button', { name: 'Obsolete', exact: true });
  const deleteAction = page.getByRole('button', { name: 'Delete', exact: true });
  await expect(obsoleteAction.or(deleteAction)).toBeVisible();

  if (await obsoleteAction.isVisible()) {
    await transitionDiskImageLifecycle(page, 'Obsolete', 'Obsolete');
  }

  await deleteAction.click();

  const deleteDialog = page.locator(...DiskImageDetailPage.deleteDialog);
  await expect(deleteDialog).toBeVisible();
  await expect(deleteDialog.locator(...DiskImageDetailPage.deleteTitle)).toContainText(
    `Delete ${diskImageName}?`,
  );
  await expect(deleteDialog.getByText(...DiskImageDetailPage.deleteWarning)).toBeVisible();

  await deleteDialog.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(deleteDialog).toBeHidden();
  await expect(page.getByRole(...DiskImagesMainPage.pageTitle)).toBeVisible();

  await includeObsoleteDiskImages(page);

  const diskImagesTable = page.getByRole(...DiskImagesMainPage.diskImagesTable);
  await expect(diskImagesTable).toBeVisible();
  // Search only for the deleted resource, then assert that its link is gone.
  await page.getByRole('textbox', { name: 'Search disk images', exact: true }).fill(diskImageName);
  await expect(diskImageLink(page, diskImageName)).toHaveCount(0, { timeout: 120_000 });
};
