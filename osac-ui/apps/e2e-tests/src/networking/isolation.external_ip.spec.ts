import { test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { externalIpName, externalIpProject } from './test_data';
import {
  expectExternalIpListScopedToProject,
  expectExternalIpListed,
  openExternalIpListPage,
  readExternalIpProjectLabel,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('keeps the tenant External IP list scoped to the owning project', async ({ page }) => {
  // The provider admin has no tenant External IPs page in this build (its
  // Networking navigation group is empty), so isolation is asserted from the
  // tenant session alone: see the coverage gaps in docs/testing/osac-ui.md.
  test.setTimeout(300_000);
  const flow = 'external-ip-isolation';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, `Starting isolation checks for "${externalIpName}".`);

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

  await testStep(flow, `Verify external IP "${externalIpName}" is visible to its tenant`, () =>
    expectExternalIpListed(page, externalIpName, externalIpProject || undefined),
  );

  const projectLabel = await testStep(flow, `Read the project that owns "${externalIpName}"`, () =>
    readExternalIpProjectLabel(page, externalIpName),
  );

  await testStep(flow, `Verify every listed external IP belongs to project "${projectLabel}"`, () =>
    expectExternalIpListScopedToProject(page, projectLabel),
  );
});
