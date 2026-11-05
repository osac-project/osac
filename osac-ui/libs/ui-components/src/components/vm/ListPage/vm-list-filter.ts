import type { TFunction } from 'i18next';

import { ComputeInstance, ComputeInstanceState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import { resolveVmStatus } from '@osac/ui-components/VmStatusLabel';

export type VmPowerStateFilter =
  | 'starting'
  | 'running'
  | 'failed'
  | 'deleting'
  | 'stopping'
  | 'stopped'
  | 'paused';

export const VM_POWER_STATE_FILTERS: readonly VmPowerStateFilter[] = [
  'starting',
  'running',
  'failed',
  'deleting',
  'stopping',
  'stopped',
  'paused',
];

const POWER_STATE_TO_INSTANCE_STATE: Record<VmPowerStateFilter, ComputeInstanceState> = {
  starting: ComputeInstanceState.STARTING,
  running: ComputeInstanceState.RUNNING,
  failed: ComputeInstanceState.FAILED,
  deleting: ComputeInstanceState.DELETING,
  stopping: ComputeInstanceState.STOPPING,
  stopped: ComputeInstanceState.STOPPED,
  paused: ComputeInstanceState.PAUSED,
};

export const isVmPowerStateFilter = (value: string): value is VmPowerStateFilter =>
  (VM_POWER_STATE_FILTERS as readonly string[]).includes(value);

export const vmPowerStateFilterLabel = (filter: VmPowerStateFilter, t: TFunction): string => {
  switch (filter) {
    case 'starting':
      return resolveVmStatus(ComputeInstanceState.STARTING, t).text;
    case 'running':
      return resolveVmStatus(ComputeInstanceState.RUNNING, t).text;
    case 'failed':
      return resolveVmStatus(ComputeInstanceState.FAILED, t).text;
    case 'deleting':
      return resolveVmStatus(ComputeInstanceState.DELETING, t).text;
    case 'stopping':
      return resolveVmStatus(ComputeInstanceState.STOPPING, t).text;
    case 'stopped':
      return resolveVmStatus(ComputeInstanceState.STOPPED, t).text;
    case 'paused':
      return resolveVmStatus(ComputeInstanceState.PAUSED, t).text;
  }
};

export type VmListFilterCriteria = {
  search?: string;
  powerState?: VmPowerStateFilter;
  instanceType?: string;
  diskImage?: string;
};

export const buildVmListFilter = (
  criteria: VmListFilterCriteria,
): CelFilter<ComputeInstance> | undefined => {
  const search = criteria.search?.trim();
  const instanceType = criteria.instanceType?.trim();
  const diskImage = criteria.diskImage?.trim();
  const hasCriteria = Boolean(search || criteria.powerState || instanceType || diskImage);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<ComputeInstance>((filter) => {
    const clauses: CelFilter<ComputeInstance>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('id').contains(search),
        ),
      );
    }

    if (criteria.powerState) {
      clauses.push(
        filter.field('status.state').equals(POWER_STATE_TO_INSTANCE_STATE[criteria.powerState]),
      );
    }

    if (instanceType) {
      clauses.push(filter.field('spec.instanceType.name').equals(instanceType));
    }

    if (diskImage) {
      clauses.push(filter.field('spec.diskImage.name').equals(diskImage));
    }

    return filter.and(...clauses);
  });
};
