import { test } from '@playwright/test';

import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { changeInstanceTypeLifecycle } from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('verifies the requested instance type lifecycle transitions', async ({ page }) => {
  test.setTimeout(120_000);
  const instanceTypeName = instanceTypeNameOrSkip();
  if (!instanceTypeName) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Instance types.
  await openInstanceTypesPage(page);

  // 3. Deprecate the Active instance type and verify the Deprecated action menu.
  const [deprecate, obsolete, reactivate] = instanceTypeTestData.lifecycleActions;
  await changeInstanceTypeLifecycle(page, instanceTypeName, deprecate);

  // 4. Reactivate it, then verify that the Active action menu is restored.
  await changeInstanceTypeLifecycle(page, instanceTypeName, reactivate);

  // 5. Deprecate it again and verify the Deprecated action menu.
  await changeInstanceTypeLifecycle(page, instanceTypeName, deprecate);

  // 6. Obsolete it and verify the Obsolete action menu.
  await changeInstanceTypeLifecycle(page, instanceTypeName, obsolete);

  // 7. Deprecate the Obsolete instance type and verify the Deprecated action menu.
  await changeInstanceTypeLifecycle(page, instanceTypeName, deprecate);

  // 8. Reactivate it and verify the Active action menu is restored.
  await changeInstanceTypeLifecycle(page, instanceTypeName, reactivate);
});
