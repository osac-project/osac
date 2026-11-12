import { expect, test } from '@playwright/test';

import { createResizeVm, deleteResizeVm } from './resize.vm.lifecycle';
import {
  type ComputeInstanceConfigurationExpectation,
  type InstanceTypeConfiguration,
  type InstanceTypeConfigurationExpectation,
  expectComputeInstanceConfiguration,
  getComputeInstanceConfiguration,
  getComputeInstanceLastRestartedAt,
} from '../../../../../libs/osac-cli/src/compute_instance';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  type VmPowerAction,
  type VmResizeTarget,
  expectVmConfiguration,
  getVmConfiguration,
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmConditionStatus,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  instanceTypeTestData,
  resizeCpuUpMemoryDownInstanceTypeTestData,
  resizeInstanceTypeTestData,
} from '../test_data';

export const restartRequiredCondition = 'RESTART REQUIRED';
export const restartInProgressCondition = 'RESTART IN PROGRESS';
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

export const resizeCpuUpMemoryDownInstanceType = {
  name: resizeCpuUpMemoryDownInstanceTypeTestData.name,
  vcpus: resizeCpuUpMemoryDownInstanceTypeTestData.vcpus,
  memoryGib: resizeCpuUpMemoryDownInstanceTypeTestData.memoryGib,
};

const formatTarget = (target: VmResizeTarget): string =>
  `${target.name} (${target.vcpus} vCPUs, ${target.memoryGib} GiB)`;

const formatConfiguration = (configuration: InstanceTypeConfiguration): string =>
  formatTarget(configuration);

const formatExpectedTarget = (target: InstanceTypeConfigurationExpectation | undefined): string => {
  if (!target) {
    return 'not asserted';
  }
  return `${target.name ?? '<any name>'} (${target.vcpus ?? '<any vCPU count>'} vCPUs, ${target.memoryGib ?? '<any memory>'} GiB)`;
};

const formatExpectedConfiguration = (expected: ComputeInstanceConfigurationExpectation): string =>
  `desired=${formatExpectedTarget(expected.desired)}, active=${formatExpectedTarget(expected.active)}`;

export const verifyResizeCliConfiguration = async (
  tenantName: string,
  vmName: string,
  expected: ComputeInstanceConfigurationExpectation,
  description: string,
): Promise<void> => {
  logProgress(
    'vm-resize-cli',
    `${description}: expecting ${formatExpectedConfiguration(expected)}.`,
  );
  await expectComputeInstanceConfiguration(tenantName, vmName, expected);
  const actual = await getComputeInstanceConfiguration(tenantName, vmName);
  logProgress(
    'vm-resize-cli',
    `${description}: observed desired=${formatConfiguration(actual.desired)}, active=${formatConfiguration(actual.active)}.`,
  );
};

export const verifyResizeUiConfiguration = async (
  page: Parameters<typeof expectVmConfiguration>[0],
  expected: VmResizeTarget,
  description: string,
): Promise<void> => {
  logProgress('vm-resize-ui', `${description}: expecting ${formatTarget(expected)}.`);
  await page.reload();
  await expectVmConfiguration(page, expected);
  const actual = await getVmConfiguration(page);
  logProgress('vm-resize-ui', `${description}: observed ${formatTarget(actual)}.`);
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
  const previousRestartedAt =
    action === 'restart' ? await getComputeInstanceLastRestartedAt(tenantName, vmName) : undefined;

  logProgress('vm-resize', `Requesting ${action} for VM "${vmName}".`);
  await runVmPowerAction(page, action);
  if (action === 'restart') {
    await waitForVmConditionStatus(
      page,
      vmName,
      restartInProgressCondition,
      'True',
      conditionTimeout,
    );
    logProgress('vm-resize-condition', `${vmName}: ${restartInProgressCondition}=True.`);
    await testStep(
      'vm-resize',
      `Verify controller processed restart for VM "${vmName}"`,
      async () => {
        await expect
          .poll(() => getComputeInstanceLastRestartedAt(tenantName, vmName), {
            message: `Wait for VM '${vmName}' last restart time to advance`,
            timeout: conditionTimeout,
            intervals: [1_000, 2_000, 5_000],
          })
          .not.toBe(previousRestartedAt);
        const restartedAt = await getComputeInstanceLastRestartedAt(tenantName, vmName);
        logProgress(
          'vm-resize-backend',
          `${vmName}: last_restarted_at advanced from ${previousRestartedAt ?? '<unset>'} to ${restartedAt ?? '<unset>'}.`,
        );
      },
    );
  }
  await waitForVmDetailsStatus(page, vmName, 'Running', lifecycleTimeout);
  logProgress('vm-resize-state', `${vmName}: lifecycle status=Running.`);
  await waitForVmConditionStatus(
    page,
    vmName,
    restartInProgressCondition,
    'False',
    conditionTimeout,
  );
  logProgress('vm-resize-condition', `${vmName}: ${restartInProgressCondition}=False.`);
  await waitForVmConditionStatus(page, vmName, restartRequiredCondition, 'False', conditionTimeout);
  logProgress('vm-resize-condition', `${vmName}: ${restartRequiredCondition}=False.`);
  await verifyResizeUiConfiguration(page, target, 'Verify active UI sizing after power action');
  await verifyResizeCliConfiguration(
    tenantName,
    vmName,
    {
      desired: target,
      active: target,
    },
    'Verify active CLI/backend sizing after power action',
  );
};

export const resizeRunningVmAndVerify = async (
  page: Parameters<typeof runVmPowerAction>[0],
  tenantName: string,
  vmName: string,
  activeBefore: VmResizeTarget,
  target: VmResizeTarget,
): Promise<void> => {
  logProgress(
    'vm-resize',
    `Requesting UI resize for VM "${vmName}": ${formatTarget(activeBefore)} → ${formatTarget(target)}.`,
  );
  await resizeVmInstanceType(page, target);
  logProgress('vm-resize-ui', `${vmName}: requested instance type ${formatTarget(target)}.`);
  await waitForVmConditionStatus(page, vmName, restartRequiredCondition, 'True', conditionTimeout);
  logProgress('vm-resize-condition', `${vmName}: ${restartRequiredCondition}=True.`);
  await verifyResizeCliConfiguration(
    tenantName,
    vmName,
    {
      desired: target,
      active: activeBefore,
    },
    'Verify requested versus active CLI/backend sizing before restart',
  );
  await restartVmAndVerify(page, tenantName, vmName, 'restart', target);
};
