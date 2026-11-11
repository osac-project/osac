import { test } from '@playwright/test';

import { createResizeVm, deleteResizeVm } from './resize.vm.lifecycle';
import { expectComputeInstanceConfiguration } from '../../../../../libs/osac-cli/src/compute_instance';
import {
  type VmPowerAction,
  type VmResizeTarget,
  expectVmConfiguration,
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmConditionStatus,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  instanceTypeTestData,
  resizeInstanceTypeTestData,
  resizeMixedInstanceTypeTestData,
} from '../test_data';

export const restartRequiredCondition = 'RESTART REQUIRED';
export const conditionTimeout = 180_000;
export const lifecycleTimeout = 300_000;

export const baseInstanceType = {
  name: instanceTypeTestData.name,
  vcpus: instanceTypeTestData.vcpus,
  memoryGib: instanceTypeTestData.memoryGib,
};

export const resizeInstanceType = {
  name: resizeInstanceTypeTestData.name,
  vcpus: resizeInstanceTypeTestData.vcpus,
  memoryGib: resizeInstanceTypeTestData.memoryGib,
};

export const resizeMixedInstanceType = {
  name: resizeMixedInstanceTypeTestData.name,
  vcpus: resizeMixedInstanceTypeTestData.vcpus,
  memoryGib: resizeMixedInstanceTypeTestData.memoryGib,
};

export const useResizeVmLifecycle = (): void => {
  test.beforeEach(async ({ page }) => {
    await createResizeVm(page);
  });

  test.afterEach(async ({ page }) => {
    await deleteResizeVm(page);
  });
};

export const restartVmAndVerify = async (
  page: Parameters<typeof runVmPowerAction>[0],
  tenantName: string,
  vmName: string,
  action: VmPowerAction,
  target: VmResizeTarget,
): Promise<void> => {
  await runVmPowerAction(page, action);
  await waitForVmDetailsStatus(page, vmName, 'Running', lifecycleTimeout);
  await waitForVmConditionStatus(page, vmName, restartRequiredCondition, 'False', conditionTimeout);
  await expectVmConfiguration(page, target);
  await expectComputeInstanceConfiguration(tenantName, vmName, {
    desired: target,
    active: target,
  });
};

export const resizeRunningVmAndVerify = async (
  page: Parameters<typeof runVmPowerAction>[0],
  tenantName: string,
  vmName: string,
  activeBefore: VmResizeTarget,
  target: VmResizeTarget,
): Promise<void> => {
  await resizeVmInstanceType(page, target);
  await waitForVmConditionStatus(page, vmName, restartRequiredCondition, 'True', conditionTimeout);
  await expectComputeInstanceConfiguration(tenantName, vmName, {
    desired: target,
    active: activeBefore,
  });
  await restartVmAndVerify(page, tenantName, vmName, 'restart', target);
};
