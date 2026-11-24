import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { externalIpName, externalIpPoolName } from './test_data';

/**
 * Non-sensitive run state, so cleanup only deletes the resources this instance
 * created. Deleting by name alone can remove a pre-existing External IP or pool
 * that happens to use the configured name.
 */
interface ExternalIpRunState {
  ip?: { name: string; id: string };
  pool?: { name: string; id: string };
}

const instance = process.env.OSAC_E2E_INSTANCE?.trim() || '1';

const statePath = (): string => path.join(os.tmpdir(), `osac-external-ip-state-${instance}.json`);

const readRunState = (): ExternalIpRunState => {
  let contents: string;
  try {
    contents = readFileSync(statePath(), 'utf8');
  } catch (error) {
    // A missing file is the normal first-run state; anything else means the
    // record cannot be trusted, so fail instead of silently dropping ids.
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return {};
    }
    throw error;
  }
  return JSON.parse(contents) as ExternalIpRunState;
};

const writeRunState = (state: ExternalIpRunState): void => {
  // Write beside the target and rename, so an interrupted write cannot truncate
  // the recorded ids.
  const target = statePath();
  const temporary = `${target}.${process.pid}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(state, null, 2)}\n`, {
    encoding: 'utf8',
    mode: 0o600,
  });
  renameSync(temporary, target);
};

/** Record the External IP this run created, replacing any earlier record. */
export const recordCreatedExternalIp = (id: string): void => {
  writeRunState({ ...readRunState(), ip: { name: externalIpName, id } });
};

/** Forget the recorded External IP, before attempting to create a new one. */
export const forgetCreatedExternalIp = (): void => {
  const { ip: _ip, ...rest } = readRunState();
  writeRunState(rest);
};

/** Record the pool this run created, replacing any earlier record. */
export const recordCreatedExternalIpPool = (id: string): void => {
  writeRunState({ ...readRunState(), pool: { name: externalIpPoolName, id } });
};

/** Forget the recorded pool, before attempting to create a new one. */
export const forgetCreatedExternalIpPool = (): void => {
  const { pool: _pool, ...rest } = readRunState();
  writeRunState(rest);
};

/** The External IP id this run created, when it recorded one. */
export const createdExternalIpId = (): string | undefined => readRunState().ip?.id;

/** The pool id this run created, when it recorded one. */
export const createdExternalIpPoolId = (): string | undefined => readRunState().pool?.id;
