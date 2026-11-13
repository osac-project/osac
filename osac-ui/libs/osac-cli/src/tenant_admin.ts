import { expect } from '@playwright/test';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { stringify } from 'yaml';

import { expectOsacCommandSucceeded, runOsacCommand } from './cli';

interface OsacUser {
  id?: string;
}

interface OsacRoleBinding {
  status?: {
    message?: string;
    state?: string;
  };
}

/** Parse the first resource from the CLI's JSON list or object response. */
const parseResource = <T>(stdout: string, description: string): T => {
  const parsed: unknown = JSON.parse(stdout);
  if (Array.isArray(parsed) && parsed[0]) {
    return parsed[0] as T;
  }
  if (parsed !== null && typeof parsed === 'object') {
    const items = (parsed as { items?: unknown }).items;
    if (Array.isArray(items) && items[0]) {
      return items[0] as T;
    }
    return parsed as T;
  }
  throw new Error(`The CLI returned an unexpected response while getting ${description}.`);
};

/** Read a tenant-admin user through the private CLI API. */
const getTenantAdminUser = async (username: string): Promise<OsacUser> => {
  const result = await runOsacCommand(['get', 'users', username, '--output', 'json']);
  expectOsacCommandSucceeded(result, `Get tenant-admin user '${username}'`);
  return parseResource<OsacUser>(result.stdout, `tenant-admin user '${username}'`);
};

/** Read a role binding through the private CLI API. */
const getRoleBinding = async (name: string): Promise<OsacRoleBinding> => {
  const result = await runOsacCommand(['get', 'rolebinding', name, '--output', 'json']);
  expectOsacCommandSucceeded(result, `Get tenant-admin role binding '${name}'`);
  return parseResource<OsacRoleBinding>(result.stdout, `role binding '${name}'`);
};

/** Normalize the role-binding state returned by the CLI. */
const roleBindingState = (roleBinding: OsacRoleBinding): string => {
  const state = roleBinding.status?.state ?? '';
  return state.endsWith('_READY') ? 'READY' : state;
};

/** Return the deterministic role-binding name used by the tenant flow. */
export const tenantAdminRoleBindingName = (tenantName: string): string =>
  `${tenantName}-tenant-admin`;

/** Grant the tenant-admin role to an existing tenant user. */
export const createTenantAdminRoleBinding = async (
  tenantName: string,
  username: string,
): Promise<void> => {
  const manifestDirectory = await mkdtemp(path.join(os.tmpdir(), 'osac-tenant-admin-'));
  const roleBindingManifestPath = path.join(manifestDirectory, 'role-binding.yaml');
  try {
    await expect
      .poll(
        async () => {
          try {
            const user = await getTenantAdminUser(username);
            return Boolean(user.id);
          } catch {
            return false;
          }
        },
        {
          message: `Wait for tenant-admin user '${username}' to be provisioned`,
          timeout: 120_000,
          intervals: [1_000, 2_000, 5_000],
        },
      )
      .toBe(true);

    const user = await getTenantAdminUser(username);
    if (!user.id) {
      throw new Error(`Tenant-admin user '${username}' has no resource ID.`);
    }

    const roleBindingName = tenantAdminRoleBindingName(tenantName);
    await writeFile(
      roleBindingManifestPath,
      stringify({
        '@type': 'type.googleapis.com/osac.public.v1.RoleBinding',
        metadata: { name: roleBindingName, tenant: tenantName },
        spec: {
          role: { name: 'tenant-admin', shared: true },
          users: [{ id: user.id }],
        },
      }),
      { encoding: 'utf8', mode: 0o600 },
    );

    const createRoleBindingResult = await runOsacCommand(['create', '-f', roleBindingManifestPath]);
    expectOsacCommandSucceeded(
      createRoleBindingResult,
      `Create tenant-admin role binding '${roleBindingName}'`,
    );

    await expect
      .poll(
        async () => {
          try {
            const roleBinding = await getRoleBinding(roleBindingName);
            const state = roleBindingState(roleBinding);
            return state === 'FAILED'
              ? `FAILED: ${roleBinding.status?.message ?? 'no failure message was provided'}`
              : state;
          } catch {
            return '';
          }
        },
        {
          message: `Wait for role binding '${roleBindingName}' to become READY`,
          timeout: 120_000,
          intervals: [1_000, 2_000, 5_000],
        },
      )
      .toBe('READY');
  } finally {
    await rm(manifestDirectory, { force: true, recursive: true });
  }
};

/** Delete a named resource when it exists, and ignore an absent resource. */
const deleteResourceIfPresent = async (resource: string, name: string): Promise<void> => {
  const existing = await runOsacCommand(['get', resource, name, '--output', 'json']);
  if (existing.exitCode !== 0) {
    return;
  }

  const result = await runOsacCommand(['delete', resource, name]);
  expectOsacCommandSucceeded(result, `Delete ${resource} '${name}'`);
};

/** Remove the tenant-admin binding and user created by the full flow. */
export const deleteTenantAdmin = async (tenantName: string, username: string): Promise<void> => {
  await deleteResourceIfPresent('rolebinding', tenantAdminRoleBindingName(tenantName));
  await deleteResourceIfPresent('user', username);
};
