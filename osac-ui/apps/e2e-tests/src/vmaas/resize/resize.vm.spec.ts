import { test } from '@playwright/test';

import {
  baseInstanceType,
  lifecycleTimeout,
  resizeInstanceType,
  resizeMixedInstanceType,
  resizeRunningVmAndVerify,
  restartVmAndVerify,
  useResizeVmLifecycle,
} from './resize_test';
import { expectComputeInstanceConfiguration } from '../../../../../libs/osac-cli/src/compute_instance';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  createInstanceType,
  deleteInstanceType,
  expectInstanceTypeNotToExist,
  openCreateInstanceTypePage,
} from '../../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../../libs/test-utils/src/main.page';
import {
  expectVmConfiguration,
  resizeVmInstanceType,
  runVmPowerAction,
  waitForVmDetailsStatus,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  resizeInstanceTypeTestData,
  resizeMixedInstanceTypeTestData,
  tenantNameOrSkip,
  vmTestName,
} from '../test_data';

test.setTimeout(900_000);
useResizeVmLifecycle();

const resizeInstanceTypes = [resizeInstanceTypeTestData, resizeMixedInstanceTypeTestData] as const;
const createdResizeInstanceTypes: string[] = [];

/** Create the resize-only instance types after the shared catalog flow. */
test.beforeAll(async ({ browser }) => {
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
      await testStep('resize-prerequisites', `Create instance type "${instanceType.name}"`, () =>
        createInstanceType(
          page,
          instanceType.name,
          instanceType.description,
          instanceType.vcpus,
          instanceType.memoryGib,
        ),
      );
      createdResizeInstanceTypes.push(instanceType.name);
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
      await testStep('resize-prerequisites', `Delete instance type "${name}"`, () =>
        deleteInstanceType(page, name),
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

test('TC-FR1-02 Resize a stopped VM from the UI, then start — new sizing is applied', async ({
  page,
}) => {
  logProgress('vm-resize', 'Starting TC-FR1-02.');
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
  await testStep('vm-resize', 'TC-FR2-02: Resize to the mixed-direction type', () =>
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
