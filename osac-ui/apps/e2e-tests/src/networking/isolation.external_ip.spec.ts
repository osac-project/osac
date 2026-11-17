import { test } from '@playwright/test';

import {
  loginAsProviderAdmin,
  loginAsTenantAdmin,
  resolveTenantAdminIdentity,
  switchIdentity,
} from './identity';
import { externalIpName, externalIpProject } from './test_data';
import {
  expectExternalIpListScopedToProject,
  expectExternalIpListed,
  openExternalIpListPage,
  readExternalIpProjectLabel,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('keeps external IPs visible only inside the owning tenant', async ({ page }) => {
  test.setTimeout(600_000);
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

  await testStep(flow, 'Log out of the tenant session', () => switchIdentity(page));

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Networking > External IPs as the provider admin', () =>
    openExternalIpListPage(page),
  );

  await testStep(
    flow,
    `Verify the provider admin sees "${externalIpName}" attributed to project "${projectLabel}"`,
    () => expectExternalIpListed(page, externalIpName, projectLabel),
  );
});
