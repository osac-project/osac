import { expect, test } from '@playwright/test';

import {
  loginAsTenantAdmin,
  resolveSecondTenantAdminIdentity,
  resolveTenantAdminIdentity,
  switchIdentity,
} from './identity';
import { externalIpName, externalIpProject, secondTenantSkipReason } from './test_data';
import {
  expectExternalIpListed,
  expectExternalIpSearchHasNoMatch,
  openExternalIpListPage,
  readExternalIpId,
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

  // Cross-tenant denial needs a second tenant identity. The provider admin
  // console cannot stand in for it: its Networking navigation group is empty,
  // so a provider admin has no tenant External IPs page.
  const secondIdentity = resolveSecondTenantAdminIdentity();
  if (!secondIdentity) {
    logProgress(flow, `Skipped cross-tenant denial: ${secondTenantSkipReason()}`);
    return;
  }

  await testStep(flow, `Log out of "${identity.username}"`, () => switchIdentity(page));

  await testStep(flow, `Log in as the second tenant admin "${secondIdentity.username}"`, () =>
    loginAsTenantAdmin(page, secondIdentity),
  );

  await testStep(flow, 'Open Networking > External IPs as the second tenant', () =>
    openExternalIpListPage(page),
  );

  await testStep(
    flow,
    `Verify external IP "${externalIpName}" is not visible to the second tenant`,
    () => expectExternalIpSearchHasNoMatch(page, externalIpName),
  );

  await testStep(
    flow,
    `Verify the paged API lookup does not resolve "${externalIpName}" for the second tenant`,
    async () => {
      expect(await readExternalIpId(page, externalIpName)).toBeUndefined();
    },
  );
});
