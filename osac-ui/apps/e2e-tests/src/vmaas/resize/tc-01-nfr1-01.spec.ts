import { test } from '@playwright/test';

import { createResizeVm, deleteResizeVm } from './resize_vm';
import { expectComputeInstanceConfiguration } from '../../../../../libs/osac-cli/src/compute_instance';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  expectVmConfiguration,
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmConditionStatus,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  instanceTypeTestData,
  resizeInstanceTypeTestData,
  tenantNameOrSkip,
  vmTestName,
} from '../test_data';

const restartRequiredCondition = 'RESTART REQUIRED';
const conditionTimeout = 180_000;
const lifecycleTimeout = 300_000;

test.setTimeout(900_000);

const baseInstanceType = {
  name: instanceTypeTestData.name,
  vcpus: instanceTypeTestData.vcpus,
  memoryGib: instanceTypeTestData.memoryGib,
};

const resizeInstanceType = {
  name: resizeInstanceTypeTestData.name,
  vcpus: resizeInstanceTypeTestData.vcpus,
  memoryGib: resizeInstanceTypeTestData.memoryGib,
};

test.beforeEach(async ({ page }) => {
  await createResizeVm(page);
});

test.afterEach(async ({ page }) => {
  await deleteResizeVm(page);
});

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
    resizeVmInstanceType(page, resizeInstanceType),
  );
  await testStep('vm-resize', 'Verify the resize requires a restart', () =>
    waitForVmConditionStatus(page, vmTestName, restartRequiredCondition, 'True', conditionTimeout),
  );
  await testStep('vm-resize', 'Verify the target is requested but not active before restart', () =>
    expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: resizeInstanceType,
      active: baseInstanceType,
    }),
  );
  await testStep('vm-resize', 'Restart the VM to apply the new sizing', async () => {
    await runVmPowerAction(page, 'restart');
    await waitForVmDetailsStatus(page, vmTestName, 'Running', lifecycleTimeout);
    await waitForVmConditionStatus(
      page,
      vmTestName,
      restartRequiredCondition,
      'False',
      conditionTimeout,
    );
    await expectVmConfiguration(page, resizeInstanceType);
    await expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: resizeInstanceType,
      active: resizeInstanceType,
    });
  });

  await testStep('vm-resize', 'Resize the running VM back to the base instance type', () =>
    resizeVmInstanceType(page, baseInstanceType),
  );
  await testStep('vm-resize', 'Verify the downsize requires a restart', () =>
    waitForVmConditionStatus(page, vmTestName, restartRequiredCondition, 'True', conditionTimeout),
  );
  await testStep('vm-resize', 'Verify the base type is requested but target remains active', () =>
    expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: baseInstanceType,
      active: resizeInstanceType,
    }),
  );
  await testStep('vm-resize', 'Restart the VM to apply the base sizing', async () => {
    await runVmPowerAction(page, 'restart');
    await waitForVmDetailsStatus(page, vmTestName, 'Running', lifecycleTimeout);
    await waitForVmConditionStatus(
      page,
      vmTestName,
      restartRequiredCondition,
      'False',
      conditionTimeout,
    );
    await expectVmConfiguration(page, baseInstanceType);
    await expectComputeInstanceConfiguration(tenantName, vmTestName, {
      desired: baseInstanceType,
      active: baseInstanceType,
    });
  });
});
