import { test } from '@playwright/test';

import { createResizeVm, deleteResizeVm } from './resize_vm';
import { instanceTypeTestData, resizeInstanceTypeTestData } from '../test_data';

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

export const useResizeVmLifecycle = (): void => {
  test.beforeEach(async ({ page }) => {
    await createResizeVm(page);
  });

  test.afterEach(async ({ page }) => {
    await deleteResizeVm(page);
  });
};
