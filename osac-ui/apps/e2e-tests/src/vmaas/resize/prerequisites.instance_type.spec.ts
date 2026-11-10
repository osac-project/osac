import { test } from '@playwright/test';

import { markResizeOwnershipIfApplicable } from './ownership';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  createInstanceType,
  expectInstanceTypeNotToExist,
  expectInstanceTypeToExist,
  openCreateInstanceTypePage,
} from '../../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../../libs/test-utils/src/main.page';
import {
  resizeInstanceTypeNameOrSkip,
  resizeInstanceTypeTestData,
  resizeMixedInstanceTypeNameOrSkip,
  resizeMixedInstanceTypeTestData,
} from '../test_data';

test('creates the configured resize instance type', async ({ page }) => {
  test.setTimeout(120_000);
  const isMixed = test.info().project.name === 'vmaas-resize-mixed-instance-type';
  const name = isMixed ? resizeMixedInstanceTypeNameOrSkip() : resizeInstanceTypeNameOrSkip();
  const data = isMixed ? resizeMixedInstanceTypeTestData : resizeInstanceTypeTestData;
  const resource = isMixed ? 'instance-type-mixed' : 'instance-type-target';

  if (!name) {
    logProgress('instance-type-create', 'Skipped: resize instance type is not configured.');
    return;
  }
  logProgress('instance-type-create', `Starting flow for "${name}".`);

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
    `Verify instance type "${name}" does not exist before creation`,
    () => expectInstanceTypeNotToExist(page, name),
  );
  await testStep('instance-type-create', 'Open the instance type creation form', () =>
    openCreateInstanceTypePage(page),
  );
  await testStep('instance-type-create', `Create instance type "${name}"`, () =>
    createInstanceType(page, name, data.description, data.vcpus, data.memoryGib),
  );
  await markResizeOwnershipIfApplicable(
    test.info().project.name,
    ['vmaas-resize-target-instance-type', 'vmaas-resize-mixed-instance-type'],
    resource,
    'instance-type-create',
  );
  await testStep(
    'instance-type-create',
    `Verify instance type "${name}" exists after creation`,
    () => expectInstanceTypeToExist(page, name),
  );
});
