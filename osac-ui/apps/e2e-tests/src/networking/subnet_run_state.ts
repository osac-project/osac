import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { subnetName } from './subnet_test_data';

/**
 * Non-sensitive run state, so cleanup only deletes the subnet this instance
 * created. Deleting by name alone could remove a pre-existing subnet that
 * happens to use the configured name.
 */
interface SubnetRunState {
  subnet?: { name: string; id: string };
  /** Network the create flow used, so later specs do not have to guess it. */
  networkName?: string;
}

const instance = process.env.OSAC_E2E_INSTANCE?.trim() || '1';

const statePath = (): string => path.join(os.tmpdir(), `osac-subnet-state-${instance}.json`);

const readRunState = (): SubnetRunState => {
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
  return JSON.parse(contents) as SubnetRunState;
};

const writeRunState = (state: SubnetRunState): void => {
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

/** Record the subnet this run created, replacing any earlier record. */
export const recordCreatedSubnet = (id: string): void => {
  writeRunState({ ...readRunState(), subnet: { name: subnetName, id } });
};

/** Forget the recorded subnet, before attempting to create a new one. */
export const forgetCreatedSubnet = (): void => {
  const { subnet: _subnet, ...rest } = readRunState();
  writeRunState(rest);
};

/** Remember the virtual network the create flow used. */
export const recordCreatedSubnetNetworkName = (networkName: string): void => {
  writeRunState({ ...readRunState(), networkName });
};

/** The subnet id this run created, when it recorded one. */
export const createdSubnetId = (): string | undefined => readRunState().subnet?.id;

/** The virtual network name this run used, when it recorded one. */
export const createdSubnetNetworkName = (): string | undefined => readRunState().networkName;
