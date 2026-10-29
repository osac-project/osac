import { test } from '@playwright/test';

import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  createInstanceType,
  expectInstanceTypeNotToExist,
  expectInstanceTypeToExist,
  openCreateInstanceTypePage,
} from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('creates an instance type from Infrastructure', async ({ page }) => {
  // Allow login and page navigation to complete on a remote cluster.
  test.setTimeout(120_000);
  const instanceTypeName = instanceTypeNameOrSkip();
  if (!instanceTypeName) {
    logProgress('instance-type-create', 'Skipped: instance_type.name is not configured.');
    return;
  }
  logProgress('instance-type-create', `Starting flow for "${instanceTypeName}".`);

  await testStep('instance-type-create', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('instance-type-create', 'Open Infrastructure > Instance types', () =>
    openInstanceTypesPage(page),
  );

  await testStep(
    'instance-type-create',
    `Verify instance type "${instanceTypeName}" does not exist before creation`,
    () => expectInstanceTypeNotToExist(page, instanceTypeName),
  );

  await testStep('instance-type-create', 'Open the instance type creation form', () =>
    openCreateInstanceTypePage(page),
  );

  await testStep('instance-type-create', `Create instance type "${instanceTypeName}"`, () =>
    createInstanceType(
      page,
      instanceTypeName,
      instanceTypeTestData.description,
      instanceTypeTestData.vcpus,
      instanceTypeTestData.memoryGib,
      instanceTypeTestData.gpu,
    ),
  );

  await testStep(
    'instance-type-create',
    `Verify instance type "${instanceTypeName}" exists after creation`,
    () => expectInstanceTypeToExist(page, instanceTypeName),
  );
});
