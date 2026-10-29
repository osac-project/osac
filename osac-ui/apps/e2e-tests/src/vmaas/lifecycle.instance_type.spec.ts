import { test } from '@playwright/test';

import { instanceTypeNameOrSkip, instanceTypeTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  changeInstanceTypeLifecycle,
  expectInstanceTypeToExist,
} from '../../../../libs/test-utils/src/instance_types.page';
import { openInstanceTypesPage } from '../../../../libs/test-utils/src/main.page';

test('verifies the requested instance type lifecycle transitions', async ({ page }) => {
  test.setTimeout(120_000);
  const instanceTypeName = instanceTypeNameOrSkip();
  if (!instanceTypeName) {
    logProgress('instance-type-lifecycle', 'Skipped: instance_type.name is not configured.');
    return;
  }
  logProgress('instance-type-lifecycle', `Starting flow for "${instanceTypeName}".`);

  await testStep('instance-type-lifecycle', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('instance-type-lifecycle', 'Open Infrastructure > Instance types', () =>
    openInstanceTypesPage(page),
  );

  await testStep(
    'instance-type-lifecycle',
    `Verify instance type "${instanceTypeName}" exists before lifecycle transitions`,
    () => expectInstanceTypeToExist(page, instanceTypeName),
  );

  const [deprecate, obsolete, reactivate] = instanceTypeTestData.lifecycleActions;
  await testStep('instance-type-lifecycle', 'Deprecate instance type', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, deprecate),
  );
  await testStep('instance-type-lifecycle', 'Reactivate instance type to Active', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, reactivate),
  );
  await testStep('instance-type-lifecycle', 'Deprecate instance type again', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, deprecate),
  );
  await testStep('instance-type-lifecycle', 'Make instance type Obsolete', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, obsolete),
  );
  await testStep('instance-type-lifecycle', 'Deprecate obsolete instance type', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, deprecate),
  );
  await testStep('instance-type-lifecycle', 'Reactivate instance type to Active', () =>
    changeInstanceTypeLifecycle(page, instanceTypeName, reactivate),
  );
});
