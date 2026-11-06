import { expect, test } from '@playwright/test';
import { access } from 'node:fs/promises';

import { tenantNameOrSkip, vmTestName } from './test_data';
import { getComputeInstanceLastRestartedAt } from '../../../../libs/osac-cli/src/compute_instance';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';
import {
  expectVmPowerButtons,
  openVmDetailsPage,
  openVmsPage,
  runVmPowerAction,
  waitForVmDetailsStatus,
  waitForVmToBeRunning,
} from '../../../../libs/test-utils/src/vms.page';

const runningButtons = { start: false, stop: true, restart: true };
const stoppedButtons = { start: true, stop: false, restart: false };

test(`stops, starts and restarts VM ${vmTestName} from its details page`, async ({ page }) => {
  // Each power action waits up to three minutes for the VM to settle.
  test.setTimeout(720_000);
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    logProgress('vm-lifecycle', 'Skipped: tenant_name is not configured.');
    return;
  }

  const credentialsFile = tenantCredentialsFilePath(tenantName);
  try {
    await access(credentialsFile);
  } catch {
    test.skip(
      true,
      `Tenant credentials were not found at ${credentialsFile}; run the full flow first.`,
    );
    return;
  }

  logProgress('vm-lifecycle', `Starting flow for "${vmTestName}".`);
  const tenantCredentials = await testStep(
    'vm-lifecycle',
    'Read generated tenant credentials',
    () => readTenantCredentials(credentialsFile),
  );

  await testStep('vm-lifecycle', 'Log in as the tenant user', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_admin'),
  );

  await testStep('vm-lifecycle', `Open VM "${vmTestName}" details`, async () => {
    await openVmsPage(page);
    await openVmDetailsPage(page, vmTestName);
  });

  await testStep(
    'vm-lifecycle',
    'Verify the VM is Running with Stop and Restart enabled',
    async () => {
      await waitForVmToBeRunning(page, vmTestName);
      await expectVmPowerButtons(page, runningButtons);
    },
  );

  await testStep('vm-lifecycle', 'Stop the VM', () => runVmPowerAction(page, vmTestName, 'stop'));
  await testStep('vm-lifecycle', 'Verify the VM is Stopped with only Start enabled', async () => {
    await waitForVmDetailsStatus(page, vmTestName, 'Stopped');
    await expectVmPowerButtons(page, stoppedButtons);
  });

  await testStep('vm-lifecycle', 'Start the VM', () => runVmPowerAction(page, vmTestName, 'start'));
  await testStep('vm-lifecycle', 'Verify the VM is Running again', async () => {
    await waitForVmToBeRunning(page, vmTestName);
    await expectVmPowerButtons(page, runningButtons);
  });

  const lastRestartedAt = await testStep('vm-lifecycle', 'Record the last restart time', () =>
    getComputeInstanceLastRestartedAt(tenantName, vmTestName),
  );
  await testStep('vm-lifecycle', 'Restart the VM', () =>
    runVmPowerAction(page, vmTestName, 'restart'),
  );
  // The details page shows no restart time, so confirm through the API that
  // the controller processed this restart request.
  await testStep('vm-lifecycle', 'Verify the controller processed the restart', () =>
    expect
      .poll(
        async () => {
          // A failed CLI read counts as "not changed yet", never as a change.
          try {
            return await getComputeInstanceLastRestartedAt(tenantName, vmTestName);
          } catch {
            return lastRestartedAt;
          }
        },
        {
          message: `Wait for VM '${vmTestName}' status.last_restarted_at to advance`,
          timeout: 180_000,
          intervals: [2_000, 5_000],
        },
      )
      .not.toBe(lastRestartedAt),
  );
  await testStep('vm-lifecycle', 'Verify the VM is Running after the restart', async () => {
    await waitForVmToBeRunning(page, vmTestName);
    await expectVmPowerButtons(page, runningButtons);
  });
});
