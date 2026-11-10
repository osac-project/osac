import { test } from '@playwright/test';

import {
  baseInstanceType,
  lifecycleTimeout,
  resizeMixedInstanceType,
  restartVmAndVerify,
  useResizeVmLifecycle,
} from './resize_test';
import { expectComputeInstanceConfiguration } from '../../../../../libs/osac-cli/src/compute_instance';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  expectVmConfiguration,
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import { tenantNameOrSkip, vmTestName } from '../test_data';

test.setTimeout(900_000);
useResizeVmLifecycle();

test('TC-FR1-02 Resize a stopped VM from the UI, then start — new sizing is applied', async ({
  page,
}) => {
  logProgress('vm-resize', `Starting TC-FR1-02 for "${vmTestName}".`);
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
  await testStep('vm-resize', 'Stop the VM', async () => {
    await runVmPowerAction(page, 'stop');
    await waitForVmDetailsStatus(page, vmTestName, 'Stopped', lifecycleTimeout);
  });
  // TC-FR2-02: Mixed-direction resize (increase CPU, decrease memory).
  await testStep('vm-resize', 'TC-FR2-02: Resize the stopped VM to the mixed-direction type', () =>
    resizeVmInstanceType(page, resizeMixedInstanceType),
  );
  await testStep('vm-resize', 'Verify the target is requested before the VM starts', () =>
    expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: resizeMixedInstanceType,
      active: baseInstanceType,
    }),
  );
  await testStep('vm-resize', 'Start the VM with the new sizing', () =>
    restartVmAndVerify(page, tenantName, vmTestName, 'start', resizeMixedInstanceType),
  );
});
