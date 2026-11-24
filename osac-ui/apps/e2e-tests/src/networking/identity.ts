import { type Page } from '@playwright/test';
import { access } from 'node:fs/promises';

import { externalIpTenantName } from './test_data';
import { logout, openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress } from '../../../../libs/test-utils/src/general/test_progress';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';

export interface E2eIdentity {
  username: string;
  password: string;
}

/** Require a value that may be trimmed (usernames, identifiers). */
const requireTrimmedEnv = (name: string): string => {
  const value = process.env[name]?.trim() ?? '';
  if (!value) {
    throw new Error(`${name} must be set to run the External IP tenant flows.`);
  }
  return value;
};

/** Require a value that must be passed through unchanged (passwords). */
const requireEnv = (name: string): string => {
  const value = process.env[name] ?? '';
  if (!value.trim()) {
    throw new Error(`${name} must be set to run the External IP tenant flows.`);
  }
  return value;
};

/**
 * Resolve the tenant-admin identity. Explicit credentials win; otherwise the
 * break-glass credentials saved by the tenant flow are read for
 * OSAC_E2E_TENANT_NAME.
 */
export const resolveTenantAdminIdentity = async (): Promise<E2eIdentity> => {
  const username = process.env.OSAC_TENANT_ADMIN_USERNAME?.trim() ?? '';
  const password = process.env.OSAC_TENANT_ADMIN_PASSWORD ?? '';
  if (Boolean(username) !== Boolean(password)) {
    throw new Error(
      'Set both OSAC_TENANT_ADMIN_USERNAME and OSAC_TENANT_ADMIN_PASSWORD, or neither. ' +
        "A partial pair would otherwise fall back to another tenant's credentials.",
    );
  }
  if (username) {
    return { username, password };
  }

  if (!externalIpTenantName) {
    throw new Error(
      'Set OSAC_E2E_TENANT_NAME to the tenant created by the tenant flow, or set ' +
        'OSAC_TENANT_ADMIN_USERNAME and OSAC_TENANT_ADMIN_PASSWORD explicitly.',
    );
  }

  const credentialsFile = tenantCredentialsFilePath(externalIpTenantName);
  try {
    await access(credentialsFile);
  } catch {
    // Do not include the tenant name or the file path: the tenant name can be
    // the replacement password, and this message reaches test and CI output.
    throw new Error(
      'Tenant credentials were not found. Run the tenant flow for the configured ' +
        'tenant first, or set OSAC_TENANT_ADMIN_USERNAME and ' +
        'OSAC_TENANT_ADMIN_PASSWORD explicitly.',
    );
  }

  const credentials = await readTenantCredentials(credentialsFile);
  return { username: credentials.username, password: credentials.password };
};

/** Log in as the tenant admin that owns the External IPs under test. */
export const loginAsTenantAdmin = async (page: Page, identity: E2eIdentity): Promise<void> => {
  await openAndLogin(page, '/', identity.username, identity.password, 'tenant_admin');
};

/** Resolve the optional second tenant admin used for cross-tenant checks. */
export const resolveSecondTenantAdminIdentity = (): E2eIdentity | undefined => {
  const username = process.env.OSAC_E2E_SECOND_TENANT_USERNAME?.trim() ?? '';
  const password = process.env.OSAC_E2E_SECOND_TENANT_PASSWORD ?? '';
  if (!username || !password) {
    return undefined;
  }
  return { username, password };
};

/** Log in as the provider admin that owns External IP pools. */
export const loginAsProviderAdmin = async (page: Page): Promise<void> => {
  const username = requireTrimmedEnv('OSAC_ADMIN_USERNAME');
  const password = requireEnv('OSAC_ADMIN_PASSWORD');
  await openAndLogin(page, '/', username, password, 'admin');
};

/** Switch the browser session to another identity. */
export const switchIdentity = async (page: Page): Promise<void> => {
  logProgress('external-ip-identity', 'Logging out before switching identity.');
  await logout(page);
};
