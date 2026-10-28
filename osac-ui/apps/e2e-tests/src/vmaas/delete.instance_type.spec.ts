import { test } from '@playwright/test';

import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  deleteInstanceType,
  ensureInstanceTypeExists,
  expectInstanceTypeNotToExist,
} from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

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

  await testStep('instance-type-delete', 'Open Infrastructure > Instance types', () =>
    openInstanceTypesPage(page),
  );

  await testStep(
    'instance-type-delete',
    `Verify instance type "${instanceTypeName}" exists before deletion`,
    async () => {
      await ensureInstanceTypeExists(
        page,
        instanceTypeName,
        instanceTypeTestData.description,
        instanceTypeTestData.vcpus,
        instanceTypeTestData.memoryGib,
        instanceTypeTestData.gpu,
      );
    },
  );

  await testStep('instance-type-delete', `Delete instance type "${instanceTypeName}"`, () =>
    deleteInstanceType(page, instanceTypeName),
  );

  await testStep(
    'instance-type-delete',
    `Verify instance type "${instanceTypeName}" does not exist after deletion`,
    () => expectInstanceTypeNotToExist(page, instanceTypeName),
  );
});
