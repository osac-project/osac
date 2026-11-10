import { test } from '@playwright/test';

import { createVmFromCatalog } from './create_vm_flow';
import { vmTestName } from './test_data';
import { testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  expectVmDetailsPage,
  expectVmDetailsToBeRunning,
  expectVmToBeRunning,
  openVmDetailsPage,
  openVmsPage,
} from '../../../../libs/test-utils/src/vms.page';

test('tenant creates a VM from the published catalog item', async ({ page }) => {
  test.setTimeout(300_000);
  await createVmFromCatalog(page, { progressLabel: 'catalog-launch', vmName: vmTestName });

  await testStep(
    'catalog-launch',
    `Verify the VM "${vmTestName}" appears in the VM list`,
    async () => {
      await openVmsPage(page);
      await expectVmToBeRunning(page, vmTestName, 60_000);
    },
  );

  await testStep('catalog-launch', `Verify the VM "${vmTestName}" details page`, async () => {
    await openVmDetailsPage(page, vmTestName);
    await expectVmDetailsPage(page, vmTestName);
    await expectVmDetailsToBeRunning(page);
  });
});
