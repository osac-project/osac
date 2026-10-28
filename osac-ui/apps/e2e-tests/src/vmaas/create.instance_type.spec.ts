import { test } from '@playwright/test';

import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import {
  createInstanceType,
  expectInstanceTypeNotToExist,
  openCreateInstanceTypePage,
} from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('creates an instance type from Infrastructure', async ({ page }) => {
  // Allow login and page navigation to complete on a remote cluster.
  test.setTimeout(120_000);
  const instanceTypeName = instanceTypeNameOrSkip();
  if (!instanceTypeName) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Instance types and verify the Instance types page.
  await openInstanceTypesPage(page);

  // 3. Fail clearly if a previous run already created the requested instance type.
  await expectInstanceTypeNotToExist(page, instanceTypeName);

  // 4. Open the create page and verify its title.
  await openCreateInstanceTypePage(page);

  // 5. Fill and submit the instance type defined in extravars.yaml.
  await createInstanceType(
    page,
    instanceTypeName,
    instanceTypeTestData.description,
    instanceTypeTestData.vcpus,
    instanceTypeTestData.memoryGib,
    instanceTypeTestData.gpu,
  );
});
