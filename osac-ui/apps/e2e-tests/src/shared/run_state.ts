import { chmodSync, readFileSync, writeFileSync } from 'node:fs';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import type { TenantIdentity } from '../../../../libs/test-utils/src/tenants.page';

export interface VmaasFullFlowRunState {
  catalogTestRunId: string;
  diskImageTestRunId: string;
  storageTierTestRunId: string;
}

const testInstance = process.env.OSAC_E2E_INSTANCE ?? '1';
if (!/^[0-9]+$/.test(testInstance)) {
  throw new Error('OSAC_E2E_INSTANCE must be a non-negative integer.');
}

const runStateFile = path.join(tmpdir(), `osac-vmaas-full-flow-state-${testInstance}.json`);
const runIdPattern = /^[a-z0-9]{1,32}$/i;

const tenantRunStateFile = (runId: string): string => {
  if (!runIdPattern.test(runId)) {
    throw new Error('Invalid VMaaS tenant run ID.');
  }
  return path.join(tmpdir(), `osac-vmaas-tenant-${testInstance}-${runId}.json`);
};

/** Record the identity returned by the successful browser Create request. */
export const writeTenantRunState = async (runId: string, tenant: TenantIdentity): Promise<void> => {
  await writeFile(tenantRunStateFile(runId), `${JSON.stringify(tenant)}\n`, { mode: 0o600 });
};

export const readTenantRunState = async (runId: string): Promise<TenantIdentity | undefined> => {
  try {
    const tenant = JSON.parse(await readFile(tenantRunStateFile(runId), 'utf8')) as TenantIdentity;
    if (
      typeof tenant?.id !== 'string' ||
      typeof tenant.creationTimestamp !== 'string' ||
      !Number.isFinite(Date.parse(tenant.creationTimestamp))
    ) {
      throw new Error('Invalid VMaaS tenant run state.');
    }
    return tenant;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return undefined;
    }
    throw error;
  }
};

export const removeTenantRunState = (runId: string): Promise<void> =>
  rm(tenantRunStateFile(runId), { force: true });

const validateRunState = (value: unknown): VmaasFullFlowRunState => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`Invalid VMaaS full-flow state in ${runStateFile}.`);
  }

  const state = value as Record<string, unknown>;
  for (const field of ['catalogTestRunId', 'diskImageTestRunId', 'storageTierTestRunId']) {
    const fieldValue = state[field];
    if (typeof fieldValue !== 'string' || !runIdPattern.test(fieldValue)) {
      throw new Error(`Invalid ${field} in VMaaS full-flow state ${runStateFile}.`);
    }
  }

  return {
    catalogTestRunId: state.catalogTestRunId as string,
    diskImageTestRunId: state.diskImageTestRunId as string,
    storageTierTestRunId: state.storageTierTestRunId as string,
  };
};

/** Read the most recent VMaaS run IDs, or fail with an actionable message. */
export const readVmaasFullFlowRunState = (): VmaasFullFlowRunState => {
  try {
    return validateRunState(JSON.parse(readFileSync(runStateFile, 'utf8')));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      throw new Error(
        `VMaaS deletion requires ${runStateFile}. Run the creation flow first, ` +
          'or provide the original OSAC_*_TEST_RUN_ID values.',
      );
    }
    throw error;
  }
};

/** Persist non-secret run IDs for a later deletion-only invocation. */
export const writeVmaasFullFlowRunState = (state: VmaasFullFlowRunState): void => {
  writeFileSync(runStateFile, `${JSON.stringify(validateRunState(state))}\n`, {
    encoding: 'utf8',
    mode: 0o600,
  });
  chmodSync(runStateFile, 0o600);
};

/** Remove the state only after the complete resource cleanup succeeds. */
export const removeVmaasFullFlowRunState = async (): Promise<void> => {
  await rm(runStateFile, { force: true });
};
