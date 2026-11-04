import { chmodSync, readFileSync, writeFileSync } from 'node:fs';
import { rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

export interface VmaasFullFlowRunState {
  catalogTestRunId: string;
  diskImageTestRunId: string;
  storageTierTestRunId: string;
}

const runStateFile = path.join(tmpdir(), 'osac-vmaas-full-flow-state.json');
const runIdPattern = /^[a-z0-9]{1,32}$/i;

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
