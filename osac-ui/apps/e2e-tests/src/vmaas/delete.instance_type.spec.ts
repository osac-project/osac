import { test } from '@playwright/test';

import { deleteInstanceTypeFlow } from './delete_flows';
import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes the requested instance type', async ({ page }) => {
  test.setTimeout(120_000);
  const instanceTypeName = instanceTypeNameOrSkip();
  if (!instanceTypeName) {
    logProgress('instance-type-delete', 'Skipped: instance_type.name is not configured.');
    return;
  }
  logProgress('instance-type-delete', `Starting flow for "${instanceTypeName}".`);

  await testStep('instance-type-delete', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('instance-type-delete', `Delete instance type "${instanceTypeName}"`, () =>
    deleteInstanceTypeFlow(
      page,
      instanceTypeName,
      instanceTypeTestData.description,
      instanceTypeTestData.vcpus,
      instanceTypeTestData.memoryGib,
      instanceTypeTestData.gpu,
    ),
  );
});
