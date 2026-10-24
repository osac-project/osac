import { type Locator, type Page, expect } from '@playwright/test';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import {
  confirmRowDeletion,
  expectRowNotToExist,
  getTableRowByCellText,
} from './general/row_assertions';
import {
  TenantsCreatePage,
  TenantsCredentialsPage,
  TenantsDeletePage,
  TenantsMainPage,
} from '../../locators/tenants';

const defaultTenantNames = ['shared', 'system'] as const;

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const exactText = (value: string): RegExp =>
  new RegExp(`^\\s*${escapeRegExp(value.normalize('NFC'))}\\s*$`);

const getTenantRow = (page: Page, tenantName: string): Locator => {
  return getTableRowByCellText(
    page.getByRole(...TenantsMainPage.tenantsTable),
    TenantsMainPage.tenantRows,
    TenantsMainPage.tenantNameCell,
    exactText(tenantName),
  );
};

const getTenantNames = async (page: Page): Promise<string[]> =>
  (
    await page
      .getByRole(...TenantsMainPage.tenantsTable)
      .locator(...TenantsMainPage.tenantRows)
      .locator(...TenantsMainPage.tenantNameCell)
      .allTextContents()
  )
    .map((tenantName) => tenantName.trim())
    .filter(Boolean);

/** Open the tenant creation page and verify its title. */
export const openCreateTenantPage = async (page: Page): Promise<void> => {
  const createTenant = page.getByRole(...TenantsMainPage.createTenant);
  await expect(createTenant).toBeVisible();
  await createTenant.click();

  await expect(page.getByRole(...TenantsCreatePage.createPageTitle)).toBeVisible();
};

/** Verify that a tenant row appears and return it for further checks. */
export const expectTenantToExist = async (
  page: Page,
  tenantName: string,
  timeout = 15_000,
): Promise<Locator> => {
  const tenantRow = getTenantRow(page, tenantName);
  await expect(tenantRow).toHaveCount(1, { timeout });
  return tenantRow;
};

/** Verify that the requested tenant is not already present on the Tenants page. */
export const expectTenantNotToExist = async (page: Page, tenantName: string): Promise<void> => {
  await expectRowNotToExist(
    getTenantRow(page, tenantName),
    `Cannot create tenant "${tenantName}": a tenant with this name already exists.`,
  );
};

/** Fill and submit the tenant creation form. */
export const createTenant = async (
  page: Page,
  tenantName: string,
  domainName = '',
): Promise<string> => {
  // Enter the required tenant name.
  await page.getByLabel(...TenantsCreatePage.name).fill(tenantName);

  if (domainName) {
    // Add the optional domain field and enter the supplied domain.
    await page.getByRole(...TenantsCreatePage.addDomain).click();
    const domain = page.getByLabel(...TenantsCreatePage.domain);
    await expect(domain).toBeVisible();
    await domain.fill(domainName);
  }

  // Submit the tenant creation form.
  await page.getByRole(...TenantsCreatePage.create).click();

  // Verify that the break-glass credentials dialog is displayed.
  const credentialsDialog = page.getByRole(...TenantsCredentialsPage.breakGlassDialog);
  await expect(credentialsDialog).toBeVisible();
  await expect(
    credentialsDialog.getByText(...TenantsCredentialsPage.breakGlassWarning),
  ).toBeVisible();

  // Verify that both generated credentials are present and non-empty.
  const copyableInputs = credentialsDialog.getByRole(...TenantsCredentialsPage.copyableInput);
  await expect(copyableInputs).toHaveCount(2);

  const username = copyableInputs.nth(0);
  const password = copyableInputs.nth(1);
  await expect(username).toHaveValue(/.+/);
  await expect(password).toHaveValue(/.+/);

  // Save the generated credentials in a tenant-specific temporary file.
  const safeTenantName = tenantName.replace(/[^a-zA-Z0-9._-]/g, '_') || 'tenant';
  const temporaryDirectory = await mkdtemp(join(tmpdir(), `osac-${safeTenantName}-`));
  const credentialsFile = join(temporaryDirectory, `${safeTenantName}-break-glass-credentials.txt`);
  await writeFile(
    credentialsFile,
    [
      `Tenant: ${tenantName}`,
      `Username: ${await username.inputValue()}`,
      `Password: ${await password.inputValue()}`,
      '',
    ].join('\n'),
    { encoding: 'utf8', mode: 0o600 },
  );

  // Close the credentials dialog and return to the Tenants main page.
  await page.getByRole(...TenantsCredentialsPage.savedCredentials).click();
  await expect(page.getByRole(...TenantsMainPage.pageTitle)).toBeVisible();

  // Locate the newly created tenant row by its exact tenant name.
  const tenantRow = await expectTenantToExist(page, tenantName);

  // Verify that the new tenant is synced and, when supplied, has the expected domain.
  await expect(tenantRow.locator(...TenantsMainPage.tenantStatusCell)).toHaveText('Synced');
  if (domainName) {
    await expect(tenantRow.locator(...TenantsMainPage.tenantPrimaryDomainCell)).toHaveText(
      exactText(domainName),
    );
  }

  return credentialsFile;
};

/** Verify that the default shared and system tenants are synced. */
export const expectDefaultTenants = async (page: Page): Promise<void> => {
  for (const tenantName of defaultTenantNames) {
    const tenantRow = getTenantRow(page, tenantName);
    await expect(tenantRow).toHaveCount(1);
    await expect(tenantRow.locator(...TenantsMainPage.tenantStatusCell)).toHaveText('Synced');
  }
};

/** Delete a tenant and verify that its row disappears from the Tenants page. */
export const deleteTenant = async (page: Page, tenantName: string): Promise<void> => {
  // Locate the tenant row by its exact name.
  let tenantRow: Locator;
  try {
    tenantRow = await expectTenantToExist(page, tenantName, 3_000);
  } catch {
    const matchingRows = await getTenantRow(page, tenantName).count();
    const availableTenantNames = await getTenantNames(page);
    const availableTenants = availableTenantNames.length
      ? availableTenantNames.map((name) => `"${name}"`).join(', ')
      : 'none';

    if (matchingRows === 0) {
      throw new Error(
        `Cannot delete tenant "${tenantName}": no matching tenant was found. ` +
          `Available tenants: ${availableTenants}. Check tenant_name in ` +
          `apps/e2e-tests/extravars.yaml or create the tenant first.`,
      );
    }

    throw new Error(
      `Cannot delete tenant "${tenantName}": found ${matchingRows} matching rows; expected exactly one.`,
    );
  }

  // Open the tenant actions menu and select Delete.
  await tenantRow.locator(...TenantsMainPage.tenantActionsToggle).click();
  await page.getByRole(...TenantsDeletePage.deleteMenuItem).click();

  // Verify the delete dialog, tenant-specific title, warning text, and row removal.
  const deleteDialog = page.locator(...TenantsDeletePage.deleteDialog);
  await confirmRowDeletion(
    tenantRow,
    deleteDialog,
    deleteDialog.locator(...TenantsDeletePage.deleteTitle),
    deleteDialog.getByText(...TenantsDeletePage.deleteWarning),
    deleteDialog.getByRole(...TenantsDeletePage.deleteButton),
    tenantName,
    'Tenant',
    120_000,
    tenantRow.locator(...TenantsMainPage.tenantStatusCell),
  );
};
