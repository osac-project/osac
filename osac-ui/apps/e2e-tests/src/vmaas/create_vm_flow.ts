import { type Page, expect, test } from '@playwright/test';
import { access } from 'node:fs/promises';

import {
  catalogTestData,
  instanceTypeTestData,
  storageTierTestData,
  tenantNameOrSkip,
} from './test_data';
import { VmCreatePage } from '../../../../libs/locators/vms';
import {
  launchCatalogItem,
  openCatalogItemDetails,
} from '../../../../libs/test-utils/src/catalog.page';
import { selectOption } from '../../../../libs/test-utils/src/general/form_controls';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';
import {
  expectVmDetailsPage,
  waitForVmToBeRunning,
} from '../../../../libs/test-utils/src/vms.page';

export interface CreateVmFromCatalogOptions {
  progressLabel: string;
  vmName: string;
  onCreationStarted?: () => void;
}

const cloudInit = '#cloud-config\nruncmd:\n  - echo vm-e2e';

/** Create a VM from the shared published catalog configuration. */
export const createVmFromCatalog = async (
  page: Page,
  options: CreateVmFromCatalogOptions,
): Promise<void> => {
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    return;
  }

  const credentialsFile = tenantCredentialsFilePath(tenantName);
  try {
    await access(credentialsFile);
  } catch {
    test.skip(
      true,
      `Tenant credentials were not found at ${credentialsFile}; run the VMaaS prerequisites first.`,
    );
    return;
  }
  options.onCreationStarted?.();

  const progressLabel = options.progressLabel;
  const tenantCredentials = await testStep(progressLabel, 'Read generated tenant credentials', () =>
    readTenantCredentials(credentialsFile),
  );

  await testStep(progressLabel, 'Log in as the tenant admin', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_admin'),
  );
  await testStep(progressLabel, 'Open the tenant Catalog', () => openCatalogPage(page));
  await testStep(progressLabel, `Open catalog item "${catalogTestData.name}"`, () =>
    openCatalogItemDetails(page, catalogTestData.name),
  );
  await testStep(progressLabel, 'Launch the VM creation wizard', () => launchCatalogItem(page));

  await testStep(progressLabel, 'Select the catalog item', async () => {
    const catalogCard = page
      .locator(...VmCreatePage.catalogItemCards)
      .filter({ hasText: catalogTestData.name })
      .first();
    await expect(catalogCard.locator(...VmCreatePage.catalogItemSelection)).toBeChecked();
    await page.getByRole(...VmCreatePage.next).click();
  });

  const projectName = await testStep(progressLabel, 'Set the project and VM name', async () => {
    const selectedProject = await selectOption(page, VmCreatePage.project, /.*/);
    await page.locator(...VmCreatePage.vmName).fill(options.vmName);
    await page.getByRole(...VmCreatePage.next).click();
    return selectedProject;
  });

  await testStep(progressLabel, 'Configure the disk image', async () => {
    const diskImage = page.locator(...VmCreatePage.diskImage);
    await expect(diskImage).toBeVisible();
    if (await diskImage.isEnabled()) {
      await selectOption(page, VmCreatePage.diskImage, catalogTestData.fields.diskImage.name);
    }
  });

  await testStep(progressLabel, 'Configure the base instance type', async () => {
    await selectOption(
      page,
      VmCreatePage.instanceType,
      `${instanceTypeTestData.name.normalize('NFC')} — ${instanceTypeTestData.vcpus} vCPUs, ${instanceTypeTestData.memoryGib} GiB`,
      0,
      true,
    );
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep(progressLabel, 'Configure the VM disks', async () => {
    await page.locator(...VmCreatePage.bootDisk).fill('100');
    await selectOption(page, VmCreatePage.bootStorageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.addDisk).click();
    await page.locator(...VmCreatePage.additionalDiskSize).fill('30');
    await selectOption(page, VmCreatePage.storageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep(progressLabel, 'Configure default networking', async () => {
    await expect(page.locator(...VmCreatePage.useTenantDefaultNetwork)).toBeChecked();
    await page.locator(...VmCreatePage.autoExternalIp).check({ force: true });
    await page.getByRole(...VmCreatePage.next).click();
  });

  const sshKeyName = await testStep(
    progressLabel,
    'Select the SSH key and configure cloud-init',
    async () => {
      const selectedSshKey = await selectOption(
        page,
        VmCreatePage.sshPublicKey,
        /^(?!No secret available$).+$/,
      );
      await page.locator(...VmCreatePage.cloudInit).fill(cloudInit);
      await page.getByRole(...VmCreatePage.next).click();
      return selectedSshKey;
    },
  );

  await testStep(progressLabel, 'Verify the VM review page', async () => {
    await expect(page.getByRole(...VmCreatePage.configurationHeading)).toBeVisible();
    await expect(page.getByRole(...VmCreatePage.storageHeading)).toBeVisible();
    await expect(page.getByText(catalogTestData.name, { exact: true })).toBeVisible();
    await expect(page.getByText(projectName, { exact: true })).toBeVisible();
    await expect(page.getByText(options.vmName, { exact: true })).toBeVisible();
    await expect(
      page.getByText(catalogTestData.fields.diskImage.name, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(instanceTypeTestData.name.normalize('NFC'), { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(sshKeyName, { exact: true })).toBeVisible();
    await expect(page.getByText('Tenant default', { exact: true })).toBeVisible();
    await expect(page.getByText('Yes', { exact: true })).toBeVisible();
    await expect(
      page.getByText(`100 GB, ${storageTierTestData.name}`, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(`30 GB, ${storageTierTestData.name}`, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(...VmCreatePage.userDataProvided)).toBeVisible();
  });

  await testStep(progressLabel, `Create VM "${options.vmName}"`, async () => {
    await page.getByRole(...VmCreatePage.create).click();
    await expectVmDetailsPage(page, options.vmName);
  });
  await testStep(progressLabel, `Wait for VM "${options.vmName}" to be Running`, () =>
    waitForVmToBeRunning(page, options.vmName),
  );
};
