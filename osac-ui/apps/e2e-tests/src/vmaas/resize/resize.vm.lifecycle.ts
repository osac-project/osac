import { type Page } from '@playwright/test';

import { expectComputeInstanceDeleted } from '../../../../../libs/osac-cli/src/compute_instance';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  deleteVmIfPresent,
  waitForVmConditionStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import { instanceTypeTestData, resizeVmTestName, tenantName } from '../../shared/test_data';
import { createVmFromCatalog } from '../create_vm_flow';

/** Create the VM used by one resize scenario from the resize catalog blocks. */
export const createResizeVm = async (page: Page, onCreationStarted?: () => void): Promise<void> => {
  logProgress(
    'resize-vm-create',
    `Creating VM "${resizeVmTestName}" with base instance type "${instanceTypeTestData.name}" (${instanceTypeTestData.vcpus} vCPUs, ${instanceTypeTestData.memoryGib} GiB).`,
  );
  await createVmFromCatalog(page, {
    progressLabel: 'resize-vm-create',
    vmName: resizeVmTestName,
    onCreationStarted,
  });
  await testStep(
    'resize-vm-create',
    `Wait for VM "${resizeVmTestName}" to report CONFIGURATION APPLIED=True`,
    () => waitForVmConditionStatus(page, resizeVmTestName, 'CONFIGURATION APPLIED', 'True'),
  );
  logProgress(
    'resize-vm-create',
    `VM "${resizeVmTestName}" is ready for resize scenarios with base sizing ${instanceTypeTestData.vcpus} vCPUs/${instanceTypeTestData.memoryGib} GiB.`,
  );
};

/** Delete the VM created for the current resize scenario. */
export const deleteResizeVm = async (page: Page): Promise<void> => {
  const errors: unknown[] = [];
  try {
    const deleted = await testStep(
      'resize-vm-delete',
      `Delete VM "${resizeVmTestName}" if present`,
      () => deleteVmIfPresent(page, resizeVmTestName),
    );
    if (!deleted) {
      logProgress('resize-vm-delete', `VM "${resizeVmTestName}" is already absent.`);
    }
  } catch (error) {
    errors.push(error);
  }

  try {
    const currentTenant = tenantName;
    if (!currentTenant) {
      throw new Error('A tenant is required to verify VM deletion through the API.');
    }
    await testStep(
      'resize-vm-delete',
      `Verify VM "${resizeVmTestName}" is absent from the API`,
      () => expectComputeInstanceDeleted(currentTenant, resizeVmTestName),
    );
  } catch (error) {
    errors.push(error);
  }
  if (errors.length === 1) {
    throw errors[0];
  }
  if (errors.length > 1) {
    throw new AggregateError(errors, 'UI and API VM deletion checks failed.');
  }
};
