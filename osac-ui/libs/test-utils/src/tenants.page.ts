import { type APIResponse, type Locator, type Page, type Response, expect } from '@playwright/test';
import { mkdir, readFile, rm, rmdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import {
  confirmRowDeletion,
  expectRowNotToExist,
  getTableRowByCellText,
} from './general/row_assertions';
import { logProgress } from './general/test_progress';
import {
  TenantsCreatePage,
  TenantsCredentialsPage,
  TenantsDeletePage,
  TenantsMainPage,
} from '../../locators/tenants';

const defaultTenantNames = ['shared', 'system'] as const;
const tenantApiPath = '/api/fulfillment/osac.private.v1.Tenants';

export interface TenantIdentity {
  id: string;
  creationTimestamp: string;
}

interface TenantRecord extends TenantIdentity {
  breakGlassSecretId?: string;
}

export interface TenantCredentials {
  username: string;
  password: string;
}

const safeTenantName = (tenantName: string): string =>
  tenantName.replace(/[^a-zA-Z0-9._-]/g, '_') || 'tenant';

const tenantCredentialsDirectoryPath = (tenantName: string): string =>
  join(tmpdir(), `osac-${safeTenantName(tenantName)}-break-glass`);

/** Return the temporary credential-file path for a tenant. */
export const tenantCredentialsFilePath = (tenantName: string): string =>
  join(
    tenantCredentialsDirectoryPath(tenantName),
    `${safeTenantName(tenantName)}-break-glass-credentials.txt`,
  );

const credentialFromFile = (contents: string, label: string): string => {
  const line = contents.split(/\r?\n/).find((entry) => entry.startsWith(`${label}:`));
  const value = line?.slice(label.length + 1).trim();
  if (!value) {
    throw new Error(`Tenant credentials file is missing ${label}.`);
  }
  return value;
};

// Filesystem errors include the credentials file path, which contains the
// tenant name (possibly also the replacement password), and testStep logs
// error messages verbatim. Rethrow them without the path.
const readCredentialsFile = async (credentialsFile: string): Promise<string> => {
  try {
    return await readFile(credentialsFile, 'utf8');
  } catch {
    throw new Error('Could not read the tenant credentials file.');
  }
};

const writeCredentialsFile = async (credentialsFile: string, contents: string): Promise<void> => {
  try {
    await writeFile(credentialsFile, contents, { encoding: 'utf8', mode: 0o600 });
  } catch {
    throw new Error('Could not write the tenant credentials file.');
  }
};

/** Read tenant credentials previously saved by createTenant. */
export const readTenantCredentials = async (
  credentialsFile: string,
): Promise<TenantCredentials> => {
  logProgress('tenant-credentials', 'Reading the generated tenant credentials.');
  const contents = await readCredentialsFile(credentialsFile);
  return {
    username: credentialFromFile(contents, 'Username'),
    password: credentialFromFile(contents, 'Password'),
  };
};

/** Replace a temporary tenant password after the first successful login. */
export const updateTenantCredentialsPassword = async (
  credentialsFile: string,
  password?: string,
): Promise<void> => {
  logProgress('tenant-credentials', 'Updating the generated tenant credentials.');
  const contents = await readCredentialsFile(credentialsFile);
  const tenantName = credentialFromFile(contents, 'Tenant');
  const replacementPassword = password?.trim() || tenantName;
  logProgress(
    'tenant-credentials',
    password?.trim()
      ? 'Using the configured replacement password.'
      : 'Using the tenant name as the replacement password.',
  );
  const updatedContents = contents.replace(
    /^Password: .*$/m,
    () => `Password: ${replacementPassword}`,
  );
  if (updatedContents === contents) {
    throw new Error('Tenant credentials file is missing Password.');
  }
  await writeCredentialsFile(credentialsFile, updatedContents);
};

/** Remove the saved credentials for a tenant, if present. */
export const removeTenantCredentials = async (tenantName: string): Promise<void> => {
  logProgress('tenant-credentials', `Removing credentials for tenant "${tenantName}".`);
  await rm(tenantCredentialsFilePath(tenantName), { force: true });
  try {
    await rmdir(tenantCredentialsDirectoryPath(tenantName));
  } catch (error) {
    const code = (error as NodeJS.ErrnoException).code;
    if (code !== 'ENOENT' && code !== 'ENOTEMPTY') {
      throw error;
    }
  }
};

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

/** Select the table view so tenant rows are available to list-based helpers. */
export const selectTenantListView = async (page: Page): Promise<void> => {
  const listView = page.locator(...TenantsMainPage.listView);
  await expect(listView).toBeVisible();
  if ((await listView.getAttribute('aria-pressed')) !== 'true') {
    await listView.click();
  }
  await expect(listView).toHaveAttribute('aria-pressed', 'true');
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
  await selectTenantListView(page);
  const tenantRow = getTenantRow(page, tenantName);
  await expect(tenantRow).toHaveCount(1, { timeout });
  return tenantRow;
};

/** Verify that the requested tenant is not already present on the Tenants page. */
export const expectTenantNotToExist = async (page: Page, tenantName: string): Promise<void> => {
  await selectTenantListView(page);
  await expectRowNotToExist(
    getTenantRow(page, tenantName),
    `Cannot create tenant "${tenantName}": a tenant with this name already exists.`,
  );
};

/** Fill and submit the tenant creation form. */
const submitTenantForm = async (page: Page, tenantName: string, domainName = ''): Promise<void> => {
  // Enter the required tenant name.
  await page.getByLabel(...TenantsCreatePage.name).fill(tenantName);

  // The primary email domain is optional, so it is only filled when supplied.
  if (domainName) {
    await page.getByLabel(...TenantsCreatePage.primaryDomain).fill(domainName);
  }

  // Walk the General and Billing steps to the review step.
  await page.getByRole(...TenantsCreatePage.next).click();
  await expect(page.getByLabel(...TenantsCreatePage.externalBillingId)).toBeVisible();
  await page.getByRole(...TenantsCreatePage.next).click();

  // Submit the tenant creation wizard.
  await page.getByRole(...TenantsCreatePage.register).click();
};

const readTenantRecord = async (
  response: APIResponse | Response,
  tenantName: string,
): Promise<TenantRecord> => {
  if (!response.ok()) {
    throw new Error(`Tenant API request failed (HTTP ${response.status()}).`);
  }

  try {
    const { object } = (await response.json()) as {
      object?: {
        id?: string;
        metadata?: { creationTimestamp?: string };
        spec?: { breakGlassCredentialsSecret?: { id?: string } };
      };
    };
    const creationTimestamp = object?.metadata?.creationTimestamp;
    if (
      object?.id === tenantName &&
      typeof creationTimestamp === 'string' &&
      Number.isFinite(Date.parse(creationTimestamp))
    ) {
      return {
        id: object.id,
        creationTimestamp,
        breakGlassSecretId: object.spec?.breakGlassCredentialsSecret?.id,
      };
    }
  } catch {
    // An older deployment can return inline credentials. Keep the response out of errors.
  }
  throw new Error('The Tenant API did not return the expected tenant identity.');
};

/** Capture the identity acknowledged by the UI's successful Create request. */
export const submitTenantAndReadIdentity = async (
  page: Page,
  tenantName: string,
): Promise<TenantIdentity> => {
  const createUrl = new URL(`${tenantApiPath}/Create`, page.url()).href;
  const [response] = await Promise.all([
    page.waitForResponse(
      (result) => result.url() === createUrl && result.request().method() === 'POST',
    ),
    submitTenantForm(page, tenantName),
  ]);
  const { id, creationTimestamp } = await readTenantRecord(response, tenantName);
  return { id, creationTimestamp };
};

/** Read identity and the secret reference through the authenticated OSAC browser session. */
export const getTenantFromBrowser = async (
  page: Page,
  tenantName: string,
): Promise<TenantRecord | undefined> => {
  const response = await page.request.post(new URL(`${tenantApiPath}/Get`, page.url()).href, {
    data: { id: tenantName },
    headers: { 'Connect-Protocol-Version': '1' },
  });
  try {
    return response.status() === 404 ? undefined : await readTenantRecord(response, tenantName);
  } finally {
    await response.dispose();
  }
};

/** Save credentials in a tenant-specific file for later Playwright projects. */
export const saveTenantCredentials = async (
  tenantName: string,
  { username, password }: TenantCredentials,
): Promise<void> => {
  const credentialsFile = tenantCredentialsFilePath(tenantName);
  const credentialsDirectory = tenantCredentialsDirectoryPath(tenantName);
  logProgress(
    'tenant-credentials',
    `Resetting the temporary credentials directory for tenant "${tenantName}".`,
  );
  await rm(credentialsDirectory, { force: true, recursive: true });
  await mkdir(credentialsDirectory, { mode: 0o700 });
  try {
    await writeFile(
      credentialsFile,
      [`Tenant: ${tenantName}`, `Username: ${username}`, `Password: ${password}`, ''].join('\n'),
      { encoding: 'utf8', flag: 'wx', mode: 0o600 },
    );
  } catch (error) {
    await rm(credentialsDirectory, { force: true, recursive: true });
    throw error;
  }
};

/** Create a tenant and save the credentials shown in the dialog. */
export const createTenant = async (
  page: Page,
  tenantName: string,
  domainName = '',
): Promise<void> => {
  await submitTenantForm(page, tenantName, domainName);

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

  await saveTenantCredentials(tenantName, {
    username: await username.inputValue(),
    password: await password.inputValue(),
  });

  // Close the credentials dialog and return to the Tenants main page.
  await page.getByRole(...TenantsCredentialsPage.savedCredentials).click();
  await expect(page.getByRole(...TenantsMainPage.pageTitle)).toBeVisible();
};

/** Verify that a tenant is synchronized and, when supplied, has the expected domain. */
export const expectTenantToBeSynced = async (
  page: Page,
  tenantName: string,
  domainName = '',
): Promise<void> => {
  const tenantRow = await expectTenantToExist(page, tenantName);
  await expect(tenantRow.locator(...TenantsMainPage.tenantStatusCell)).toHaveText('Synced');
  if (domainName) {
    await expect(tenantRow.locator(...TenantsMainPage.tenantPrimaryDomainCell)).toHaveText(
      exactText(domainName),
    );
  }
};

/** Verify that the default shared and system tenants are synced. */
export const expectDefaultTenants = async (page: Page): Promise<void> => {
  await selectTenantListView(page);
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
    300_000,
    tenantRow.locator(...TenantsMainPage.tenantStatusCell),
  );

  await removeTenantCredentials(tenantName);
};
