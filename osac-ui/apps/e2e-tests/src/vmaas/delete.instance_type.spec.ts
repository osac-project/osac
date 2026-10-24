import { test } from '@playwright/test';

import { instanceTypeTestData, tenantNameOrSkip } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import {
  deleteInstanceType,
  ensureInstanceTypeExists,
} from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('deletes the requested instance type', async ({ page }) => {
  test.setTimeout(120_000);
  if (!tenantNameOrSkip()) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Instance types and delete the requested instance type.
  await openInstanceTypesPage(page);
  await ensureInstanceTypeExists(
    page,
    instanceTypeTestData.name,
    instanceTypeTestData.description,
    instanceTypeTestData.vcpus,
    instanceTypeTestData.memoryGib,
    instanceTypeTestData.gpu,
  );
  await deleteInstanceType(page, instanceTypeTestData.name);
});
