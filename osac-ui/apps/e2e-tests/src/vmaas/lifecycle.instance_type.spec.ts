import { test } from '@playwright/test';

import { instanceTypeTestData } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { changeInstanceTypeLifecycle } from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('deprecates and obsoletes the requested instance type', async ({ page }) => {
  test.setTimeout(120_000);

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Instance types.
  await openInstanceTypesPage(page);

  // 3. Deprecate the instance type and verify the Deprecated action menu.
  const [deprecate, obsolete] = instanceTypeTestData.lifecycleActions;
  await changeInstanceTypeLifecycle(page, instanceTypeTestData.name, deprecate);

  // 4. Obsolete the instance type and verify the Obsolete action menu.
  await changeInstanceTypeLifecycle(page, instanceTypeTestData.name, obsolete);
});
