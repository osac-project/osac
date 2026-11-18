import { test } from '@playwright/test';

import {
  baseInstanceType,
  lifecycleTimeout,
  resizeCpuUpMemoryDownInstanceType,
  resizeInstanceType,
  resizeRunningVmAndVerify,
  restartVmAndVerify,
  useResizeVmLifecycle,
  verifyResizeCliConfiguration,
  verifyResizeUiConfiguration,
} from './resize.helpers';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  createInstanceType,
  deleteInstanceTypeIfPresent,
  expectInstanceTypeNotToExist,
  openCreateInstanceTypePage,
} from '../../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../../libs/test-utils/src/main.page';
import {
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  resizeCpuUpMemoryDownInstanceTypeTestData,
  resizeInstanceTypeTestData,
  resizeVmTestName,
  tenantNameOrSkip,
} from '../test_data';

test.setTimeout(900_000);
useResizeVmLifecycle();

const resizeInstanceTypes = [
  resizeInstanceTypeTestData,
  resizeCpuUpMemoryDownInstanceTypeTestData,
] as const;
const createdResizeInstanceTypes: string[] = [];

/** Create the resize-only instance types after the shared catalog flow. */
test.beforeAll(async ({ browser }, testInfo) => {
  testInfo.setTimeout(900_000);
  const page = await browser.newPage();
  try {
    await testStep('resize-prerequisites', 'Log in as the admin test user', () =>
      openAndLogin(
        page,
        '/',
        process.env.OSAC_ADMIN_USERNAME ?? '',
        process.env.OSAC_ADMIN_PASSWORD ?? '',
        'admin',
      ),
    );
    await testStep('resize-prerequisites', 'Open Infrastructure > Instance types', () =>
      openInstanceTypesPage(page),
    );

    for (const instanceType of resizeInstanceTypes) {
      await testStep(
        'resize-prerequisites',
        `Verify instance type "${instanceType.name}" does not exist`,
        () => expectInstanceTypeNotToExist(page, instanceType.name),
      );
      await testStep(
        'resize-prerequisites',
        `Open the creation form for "${instanceType.name}"`,
        () => openCreateInstanceTypePage(page),
      );
      createdResizeInstanceTypes.push(instanceType.name);
      await testStep(
        'resize-prerequisites',
        `Create instance type "${instanceType.name}" (${instanceType.vcpus} vCPUs, ${instanceType.memoryGib} GiB)`,
        () =>
          createInstanceType(
            page,
            instanceType.name,
            instanceType.description,
            instanceType.vcpus,
            instanceType.memoryGib,
          ),
      );
    }
  } finally {
    await page.close();
  }
});

/** Remove only the resize instance types created by this Playwright run. */
test.afterAll(async ({ browser }) => {
  if (createdResizeInstanceTypes.length === 0) {
    return;
  }

  const page = await browser.newPage();
  try {
    await testStep('resize-prerequisites', 'Log in as the admin test user for cleanup', () =>
      openAndLogin(
        page,
        '/',
        process.env.OSAC_ADMIN_USERNAME ?? '',
        process.env.OSAC_ADMIN_PASSWORD ?? '',
        'admin',
      ),
    );
    await testStep('resize-prerequisites', 'Open instance types for cleanup', () =>
      openInstanceTypesPage(page),
    );

    for (const name of [...createdResizeInstanceTypes].reverse()) {
      await testStep(
        'resize-prerequisites',
        `Delete instance type "${name}" if present`,
        async () => {
          const deleted = await deleteInstanceTypeIfPresent(page, name);
          logProgress(
            'resize-prerequisites',
            deleted
              ? `Deleted resize instance type "${name}".`
              : `Resize instance type "${name}" was not present; cleanup continued.`,
          );
        },
      );
    }
  } finally {
    await page.close();
  }
});

test('TC-NFR1-01 Resize a running VM from the UI — restart required', async ({ page }) => {
  logProgress('vm-resize', 'Starting TC-NFR1-01.');
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    return;
  }

  await testStep(
    'vm-resize',
    `Verify the initial UI sizing: ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () => verifyResizeUiConfiguration(page, baseInstanceType, 'Verify initial UI sizing'),
  );
  await testStep(
    'vm-resize',
    `Verify initial CLI/backend sizing: desired and active are ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () =>
      verifyResizeCliConfiguration(
        tenantName,
        resizeVmTestName,
        {
          desired: baseInstanceType,
          active: baseInstanceType,
        },
        'Verify initial CLI/backend sizing',
      ),
  );
  await testStep(
    'vm-resize',
    `Resize running VM from ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB) to ${resizeInstanceType.name} (${resizeInstanceType.vcpus} vCPUs, ${resizeInstanceType.memoryGib} GiB)`,
    () =>
      resizeRunningVmAndVerify(
        page,
        tenantName,
        resizeVmTestName,
        baseInstanceType,
        resizeInstanceType,
      ),
  );
  await testStep(
    'vm-resize',
    `Resize running VM back to ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () =>
      resizeRunningVmAndVerify(
        page,
        tenantName,
        resizeVmTestName,
        resizeInstanceType,
        baseInstanceType,
      ),
  );
});

test('TC-FR1-02 Resize a stopped VM from the UI, then start — new sizing is applied', async ({
  page,
}) => {
  logProgress('vm-resize', 'Starting TC-FR1-02.');
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    return;
  }

  await testStep(
    'vm-resize',
    `Verify the initial UI sizing: ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () => verifyResizeUiConfiguration(page, baseInstanceType, 'Verify initial UI sizing'),
  );
  await testStep(
    'vm-resize',
    `Verify initial CLI/backend sizing: desired and active are ${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () =>
      verifyResizeCliConfiguration(
        tenantName,
        resizeVmTestName,
        {
          desired: baseInstanceType,
          active: baseInstanceType,
        },
        'Verify initial CLI/backend sizing',
      ),
  );
  await testStep(
    'vm-resize',
    `Stop VM "${resizeVmTestName}" before the stopped-VM resize`,
    async () => {
      await runVmPowerAction(page, 'stop');
      await waitForVmDetailsStatus(page, resizeVmTestName, 'Stopped', lifecycleTimeout);
      logProgress('vm-resize-state', `${resizeVmTestName}: lifecycle status=Stopped.`);
    },
  );
  // TC-FR2-02: Increase CPU while decreasing memory.
  await testStep(
    'vm-resize',
    `TC-FR2-02: Increase CPU and decrease memory: ${baseInstanceType.vcpus} → ${resizeCpuUpMemoryDownInstanceType.vcpus} vCPUs; ${baseInstanceType.memoryGib} → ${resizeCpuUpMemoryDownInstanceType.memoryGib} GiB`,
    async () => {
      await resizeVmInstanceType(page, resizeCpuUpMemoryDownInstanceType);
      logProgress(
        'vm-resize-ui',
        `${resizeVmTestName}: requested instance type ${resizeCpuUpMemoryDownInstanceType.name} (${resizeCpuUpMemoryDownInstanceType.vcpus} vCPUs, ${resizeCpuUpMemoryDownInstanceType.memoryGib} GiB).`,
      );
    },
  );
  await testStep(
    'vm-resize',
    `Verify requested versus active CLI/backend sizing before start: desired=${resizeCpuUpMemoryDownInstanceType.name} (${resizeCpuUpMemoryDownInstanceType.vcpus} vCPUs, ${resizeCpuUpMemoryDownInstanceType.memoryGib} GiB), active=${baseInstanceType.name} (${baseInstanceType.vcpus} vCPUs, ${baseInstanceType.memoryGib} GiB)`,
    () =>
      verifyResizeCliConfiguration(
        tenantName,
        resizeVmTestName,
        {
          desired: resizeCpuUpMemoryDownInstanceType,
          active: baseInstanceType,
        },
        'Verify requested versus active CLI/backend sizing before start',
      ),
  );
  await testStep(
    'vm-resize',
    `Start VM "${resizeVmTestName}" and verify active sizing ${resizeCpuUpMemoryDownInstanceType.name} (${resizeCpuUpMemoryDownInstanceType.vcpus} vCPUs, ${resizeCpuUpMemoryDownInstanceType.memoryGib} GiB)`,
    () =>
      restartVmAndVerify(
        page,
        tenantName,
        resizeVmTestName,
        'start',
        resizeCpuUpMemoryDownInstanceType,
      ),
  );
});
