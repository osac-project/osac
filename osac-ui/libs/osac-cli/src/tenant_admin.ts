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
    state?: string;
  };
}

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

const getTenantAdminUser = async (username: string): Promise<OsacUser> => {
  const result = await runOsacCommand(['get', 'users', username, '--output', 'json']);
  expectOsacCommandSucceeded(result, `Get tenant-admin user '${username}'`);
  return parseResource<OsacUser>(result.stdout, `tenant-admin user '${username}'`);
};

const getRoleBinding = async (name: string): Promise<OsacRoleBinding> => {
  const result = await runOsacCommand(['get', 'rolebinding', name, '--output', 'json']);
  expectOsacCommandSucceeded(result, `Get tenant-admin role binding '${name}'`);
  return parseResource<OsacRoleBinding>(result.stdout, `role binding '${name}'`);
};

const roleBindingState = (roleBinding: OsacRoleBinding): string => {
  const state = roleBinding.status?.state ?? '';
  return state.endsWith('_READY') ? 'READY' : state;
};

export const tenantAdminRoleBindingName = (tenantName: string): string =>
  `${tenantName}-tenant-admin`;

/** Create a tenant user and grant it the tenant-admin role. */
export const createTenantAdmin = async (
  tenantName: string,
  username: string,
  password: string,
): Promise<void> => {
  const manifestDirectory = await mkdtemp(path.join(os.tmpdir(), 'osac-tenant-admin-'));
  const userManifestPath = path.join(manifestDirectory, 'user.yaml');
  const roleBindingManifestPath = path.join(manifestDirectory, 'role-binding.yaml');
  try {
    await writeFile(
      userManifestPath,
      stringify({
        '@type': 'type.googleapis.com/osac.public.v1.User',
        metadata: { name: username, tenant: tenantName },
        spec: {
          username,
          email: `${username}@${tenantName}.osac.local`,
          enabled: true,
          credentials: { password, temporary_password: false },
        },
      }),
      { encoding: 'utf8', mode: 0o600 },
    );

    const createUserResult = await runOsacCommand(['create', '-f', userManifestPath]);
    expectOsacCommandSucceeded(createUserResult, `Create tenant-admin user '${username}'`);

    await expect
      .poll(
        async () => {
          try {
            const user = await getTenantAdminUser(username);
            return user.id ? user : undefined;
          } catch {
            return undefined;
          }
        },
        {
          message: `Wait for tenant-admin user '${username}' to become available`,
          timeout: 120_000,
          intervals: [1_000, 2_000, 5_000],
        },
      )
      .toMatchObject({ id: expect.any(String) });

    const user = await getTenantAdminUser(username);
    if (!user.id) {
      throw new Error(`Tenant-admin user '${username}' has no identifier.`);
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
            return roleBindingState(await getRoleBinding(roleBindingName));
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
