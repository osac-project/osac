import { test } from '@playwright/test';

import {
  baseInstanceType,
  resizeInstanceType,
  resizeRunningVmAndVerify,
  useResizeVmLifecycle,
} from './resize_test';
import { expectComputeInstanceConfiguration } from '../../../../../libs/osac-cli/src/compute_instance';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import { expectVmConfiguration } from '../../../../../libs/test-utils/src/vms.page';
import { tenantNameOrSkip, vmTestName } from '../test_data';

test.setTimeout(900_000);
useResizeVmLifecycle();

test('TC-NFR1-01 Resize a running VM from the UI — restart required', async ({ page }) => {
  logProgress('vm-resize', `Starting TC-NFR1-01 for "${vmTestName}".`);
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    return;
  }

  await testStep('vm-resize', 'Verify the initial instance type and sizing', () =>
    expectVmConfiguration(page, baseInstanceType),
  );
  await testStep('vm-resize', 'Verify the initial active sizing through the CLI', () =>
    expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: baseInstanceType,
      active: baseInstanceType,
    }),
  );
  await testStep('vm-resize', 'Resize the running VM to the target instance type', () =>
    resizeRunningVmAndVerify(page, tenantName, vmTestName, baseInstanceType, resizeInstanceType),
  );

  await testStep('vm-resize', 'Resize the running VM back to the base instance type', () =>
    resizeRunningVmAndVerify(page, tenantName, vmTestName, resizeInstanceType, baseInstanceType),
  );
});
