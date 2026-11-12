import { type Page } from '@playwright/test';

import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  deleteVmIfPresent,
  waitForVmConditionStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import { createVmFromCatalog } from '../create_vm_flow';
import { resizeVmTestName } from '../test_data';

/** Create the VM used by one resize scenario from the resize catalog blocks. */
export const createResizeVm = async (page: Page): Promise<void> => {
  await createVmFromCatalog(page, {
    progressLabel: 'resize-vm-create',
    vmName: resizeVmTestName,
  });
  await testStep(
    'resize-vm-create',
    `Wait for VM "${resizeVmTestName}" configuration to be applied`,
    () => waitForVmConditionStatus(page, resizeVmTestName, 'CONFIGURATION APPLIED', 'True'),
  );
};

/** Delete the VM created for the current resize scenario. */
export const deleteResizeVm = async (page: Page): Promise<void> => {
  const deleted = await testStep(
    'resize-vm-delete',
    `Delete VM "${resizeVmTestName}" if present`,
    () => deleteVmIfPresent(page, resizeVmTestName),
  );
  if (!deleted) {
    logProgress('resize-vm-delete', `VM "${resizeVmTestName}" is already absent.`);
  }
};
