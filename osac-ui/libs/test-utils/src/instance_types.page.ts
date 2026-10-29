import { type Locator, type Page, expect } from '@playwright/test';

import {
  confirmRowDeletion,
  expectRowNotToExist,
  getTableRowByCellText,
} from './general/row_assertions';
import type { LifecycleAction } from './lifecycle';
import {
  InstanceTypesActionsMenu,
  InstanceTypesCreatePage,
  InstanceTypesDeletePage,
  InstanceTypesDetailPage,
  InstanceTypesMainPage,
} from '../../locators/instance_types';
import {
  instanceTypeLifecycleActionsByState,
  instanceTypeLifecycleNextState,
} from '../../ui-components/src/components/InstanceType/instanceTypeLifecycle';

export interface InstanceTypeGpu {
  count: number;
  resourceName: string;
  pciDeviceSelector: string;
}

export type InstanceTypeLifecycleAction = LifecycleAction;

type InstanceTypeLifecycleState = 'Active' | 'Deprecated' | 'Obsolete';
type InstanceTypeMenuAction = InstanceTypeLifecycleAction | 'Delete';
type MenuItemLocator = readonly [
  role: 'menuitem',
  options: { readonly name: string; readonly exact: true },
];

const lifecycleActionLocators: Record<InstanceTypeMenuAction, MenuItemLocator> = {
  Deprecate: InstanceTypesActionsMenu.deprecate,
  Obsolete: InstanceTypesActionsMenu.obsolete,
  Reactivate: InstanceTypesActionsMenu.reactivate,
  Delete: InstanceTypesActionsMenu.delete,
};

const allMenuActions: readonly InstanceTypeMenuAction[] = [
  'Deprecate',
  'Obsolete',
  'Reactivate',
  'Delete',
];

const expectedMenuActions = (lifecycleState: InstanceTypeLifecycleState) => {
  return instanceTypeLifecycleActionsByState[lifecycleState];
};

const setNumberWithPlusButton = async (
  plusButton: Locator,
  value: number,
  fieldName: string,
): Promise<void> => {
  if (!Number.isInteger(value) || value < 1) {
    throw new Error(`${fieldName} must be a positive integer.`);
  }

  for (let click = 0; click < value; click += 1) {
    await plusButton.click();
  }
};

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const exactText = (value: string): RegExp =>
  new RegExp(`^${escapeRegExp(value.normalize('NFC'))}$`);

const expectDetailValue = async (
  detailsCard: Locator,
  label: string,
  expectedValue: string,
): Promise<void> => {
  const detailGroup = detailsCard
    .locator(...InstanceTypesDetailPage.detailsGroups)
    .filter({ hasText: label });
  await expect(detailGroup).toHaveCount(1);
  await expect(detailGroup.locator(...InstanceTypesDetailPage.detailValue)).toHaveText(
    exactText(expectedValue),
  );
};

const getInstanceTypeRow = (page: Page, name: string): Locator => {
  return getTableRowByCellText(
    page.getByRole(...InstanceTypesMainPage.instanceTypesTable),
    InstanceTypesMainPage.instanceTypeRows,
    InstanceTypesMainPage.instanceTypeNameCell,
    exactText(name),
  );
};

const getMenuItem = (page: Page, action: InstanceTypeMenuAction): Locator => {
  const [role, options] = lifecycleActionLocators[action];
  return page.getByRole(role, options);
};

const expectActionMenu = async (
  page: Page,
  lifecycleState: InstanceTypeLifecycleState,
): Promise<void> => {
  const availableActions = expectedMenuActions(lifecycleState);
  for (const action of availableActions) {
    const menuItem = getMenuItem(page, action);
    await expect(menuItem).toBeVisible();
  }

  for (const action of allMenuActions) {
    if (!availableActions.includes(action)) {
      await expect(getMenuItem(page, action)).toHaveCount(0);
    }
  }
};

const getLifecycleState = async (instanceTypeRow: Locator): Promise<InstanceTypeLifecycleState> => {
  const lifecycleState = (
    await instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeLifecycleCell).textContent()
  )?.trim();
  if (
    lifecycleState !== 'Active' &&
    lifecycleState !== 'Deprecated' &&
    lifecycleState !== 'Obsolete'
  ) {
    throw new Error(
      `Could not determine the lifecycle state for the instance type row; received "${lifecycleState ?? ''}".`,
    );
  }

  return lifecycleState;
};

/** Open the instance type creation page and verify its title. */
export const openCreateInstanceTypePage = async (page: Page): Promise<void> => {
  await page.getByRole(...InstanceTypesMainPage.createInstanceType).click();

  await expect(page.getByRole(...InstanceTypesCreatePage.pageTitle)).toBeVisible();
};

/** Verify that the requested instance type is not already present in the list. */
export const expectInstanceTypeNotToExist = async (page: Page, name: string): Promise<void> => {
  await expectRowNotToExist(
    getInstanceTypeRow(page, name),
    `Cannot create instance type "${name}": an instance type with this name already exists.`,
  );
};

/** Verify that the requested instance type is present in the list. */
export const expectInstanceTypeToExist = async (page: Page, name: string): Promise<void> => {
  await expect(getInstanceTypeRow(page, name)).toHaveCount(1);
};

/** Fill and submit the instance type creation form. */
export const createInstanceType = async (
  page: Page,
  name: string,
  description: string,
  vcpus: number,
  memoryGib: number,
  gpu?: InstanceTypeGpu,
): Promise<void> => {
  // Enter the instance type name and description.
  await page.locator(...InstanceTypesCreatePage.name).fill(name);
  await page.locator(...InstanceTypesCreatePage.description).fill(description);

  // Set vCPUs and memory by clicking each field's Plus button.
  await setNumberWithPlusButton(page.locator(...InstanceTypesCreatePage.vcpusPlus), vcpus, 'vCPUs');
  await setNumberWithPlusButton(
    page.locator(...InstanceTypesCreatePage.memoryGibPlus),
    memoryGib,
    'Memory (GiB)',
  );
  await expect(page.locator(...InstanceTypesCreatePage.vcpus)).toHaveValue(String(vcpus));
  await expect(page.locator(...InstanceTypesCreatePage.memoryGib)).toHaveValue(String(memoryGib));

  if (gpu) {
    // Fill all GPU fields when GPU data is supplied.
    await setNumberWithPlusButton(
      page.locator(...InstanceTypesCreatePage.gpuCountPlus),
      gpu.count,
      'GPU count',
    );
    await page.locator(...InstanceTypesCreatePage.gpuResourceName).fill(gpu.resourceName);
    await page.locator(...InstanceTypesCreatePage.gpuPciDeviceSelector).fill(gpu.pciDeviceSelector);
    await expect(page.locator(...InstanceTypesCreatePage.gpuCount)).toHaveValue(String(gpu.count));
  }

  // Submit the instance type creation form.
  await page.getByRole(...InstanceTypesCreatePage.create).click();

  // Verify that the created instance type opens with all supplied data.
  await expectInstanceTypeCreated(page, name, description, vcpus, memoryGib, gpu);

  // Return to the list through the detail-page breadcrumb and verify the table row.
  await expectInstanceTypeListed(page, name, vcpus, memoryGib, gpu);
};

/** Ensure the requested instance type exists before a standalone delete run. */
export const ensureInstanceTypeExists = async (
  page: Page,
  name: string,
  description: string,
  vcpus: number,
  memoryGib: number,
  gpu?: InstanceTypeGpu,
): Promise<void> => {
  const instanceTypeRow = getInstanceTypeRow(page, name);

  try {
    await expect(instanceTypeRow).toHaveCount(1, { timeout: 3_000 });
    return;
  } catch {
    const matchingRows = await instanceTypeRow.count();
    if (matchingRows !== 0) {
      throw new Error(
        `Expected exactly one instance type named "${name}" before deletion, ` +
          `but found ${matchingRows}.`,
      );
    }
  }

  // Keep deletion self-contained when the requested instance type is absent,
  // while leaving any unrelated rows in the table unchanged.
  await openCreateInstanceTypePage(page);
  await createInstanceType(page, name, description, vcpus, memoryGib, gpu);
};

/** Verify the created instance type details and active lifecycle state. */
export const expectInstanceTypeCreated = async (
  page: Page,
  name: string,
  description: string,
  vcpus: number,
  memoryGib: number,
  gpu?: InstanceTypeGpu,
): Promise<void> => {
  // Verify that the detail page opens with the requested name and description.
  await expect(page.locator(...InstanceTypesDetailPage.pageTitle)).toHaveText(exactText(name));
  const descriptionLocator = page.locator(...InstanceTypesDetailPage.description);
  if (description) {
    await expect(descriptionLocator).toHaveText(exactText(description));
  } else {
    await expect(descriptionLocator).toHaveCount(0);
  }

  // Verify the requested values and Active lifecycle state in the Details card.
  const detailsCard = page.locator(...InstanceTypesDetailPage.cards).filter({ hasText: 'Details' });
  await expect(detailsCard).toHaveCount(1);
  await expectDetailValue(detailsCard, 'Name', name);
  await expectDetailValue(detailsCard, 'vCPUs', String(vcpus));
  await expectDetailValue(detailsCard, 'Memory (GiB)', String(memoryGib));
  await expectDetailValue(detailsCard, 'Lifecycle state', 'Active');

  // Verify either the supplied GPU values or the no-GPU state.
  const gpuCard = page.locator(...InstanceTypesDetailPage.cards).filter({ hasText: 'GPU' });
  await expect(gpuCard).toHaveCount(1);
  if (gpu) {
    await expectDetailValue(gpuCard, 'Count', String(gpu.count));
    await expectDetailValue(gpuCard, 'Resource name', gpu.resourceName);
    await expectDetailValue(gpuCard, 'PCI device selector', gpu.pciDeviceSelector);
  } else {
    await expect(gpuCard.getByText(...InstanceTypesDetailPage.noGpuMessage)).toBeVisible();
  }
};

/** Return to the instance types list and verify the created row. */
export const expectInstanceTypeListed = async (
  page: Page,
  name: string,
  vcpus: number,
  memoryGib: number,
  gpu?: InstanceTypeGpu,
): Promise<void> => {
  // Click the Instance types breadcrumb on the detail page, not the sidebar menu item.
  await page.getByRole(...InstanceTypesDetailPage.instanceTypesBreadcrumb).click();
  await expect(page.getByRole(...InstanceTypesMainPage.pageTitle)).toBeVisible();

  // Locate the created row by its exact name.
  const instanceTypeRow = getInstanceTypeRow(page, name);
  await expect(instanceTypeRow).toHaveCount(1);

  // Verify the created row's lifecycle state and resource values.
  await expect(instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeNameCell)).toHaveText(
    exactText(name),
  );
  await expect(
    instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeLifecycleCell),
  ).toHaveText(exactText('Active'));
  await expect(instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeVcpusCell)).toHaveText(
    exactText(String(vcpus)),
  );
  await expect(
    instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeMemoryGibCell),
  ).toHaveText(exactText(String(memoryGib)));
  await expect(instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeGpusCell)).toHaveText(
    exactText(gpu ? String(gpu.count) : '—'),
  );
};

/** Change an instance type to Deprecated or Obsolete and verify its action menu. */
export const changeInstanceTypeLifecycle = async (
  page: Page,
  name: string,
  action: InstanceTypeLifecycleAction,
): Promise<void> => {
  // Locate the requested instance type and read its current lifecycle state.
  const instanceTypeRow = getInstanceTypeRow(page, name);
  await expect(instanceTypeRow).toHaveCount(1);
  const currentState = await getLifecycleState(instanceTypeRow);

  // Open the three-dot action menu and verify the options for the current state.
  await instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeActionsToggle).click();
  await expectActionMenu(page, currentState);

  // Select the requested lifecycle action and wait for the row to reach its new state.
  await getMenuItem(page, action).click();
  const expectedState = instanceTypeLifecycleNextState[action];
  await expect(
    instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeLifecycleCell),
  ).toHaveText(exactText(expectedState));

  // Reopen the three-dot action menu and verify the options for the new state.
  await instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeActionsToggle).click();
  await expectActionMenu(page, expectedState);
  await instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeActionsToggle).click();
};

/** Make an instance type Obsolete when needed, delete it, and wait for its row to disappear. */
export const deleteInstanceType = async (page: Page, name: string): Promise<void> => {
  // Locate the requested instance type and read its current lifecycle state.
  const instanceTypeRow = getInstanceTypeRow(page, name);
  await expect(instanceTypeRow).toHaveCount(1);

  const currentState = await getLifecycleState(instanceTypeRow);

  if (currentState !== 'Obsolete') {
    // Reuse the lifecycle transition flow before opening the delete menu.
    await changeInstanceTypeLifecycle(page, name, 'Obsolete');
  }

  // Open the three-dot action menu, verify the Obsolete options, and select Delete.
  await instanceTypeRow.locator(...InstanceTypesMainPage.instanceTypeActionsToggle).click();
  await expectActionMenu(page, 'Obsolete');
  await getMenuItem(page, 'Delete').click();

  // Verify the delete confirmation dialog and wait for the row to disappear.
  const deleteDialog = page.locator(...InstanceTypesDeletePage.deleteDialog);
  await confirmRowDeletion(
    instanceTypeRow,
    deleteDialog,
    deleteDialog.locator(...InstanceTypesDeletePage.deleteTitle),
    deleteDialog.getByText(...InstanceTypesDeletePage.deleteWarning),
    deleteDialog.getByRole(...InstanceTypesDeletePage.deleteButton),
    name,
    'Instance type',
    60_000,
  );
};
