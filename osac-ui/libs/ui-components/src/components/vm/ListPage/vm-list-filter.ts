import { ComputeInstance, ComputeInstanceState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

import { type VmPowerStateFilter } from '../vmInstanceDisplay';

const POWER_STATE_TO_INSTANCE_STATE: Record<VmPowerStateFilter, ComputeInstanceState> = {
  starting: ComputeInstanceState.STARTING,
  running: ComputeInstanceState.RUNNING,
  failed: ComputeInstanceState.FAILED,
  deleting: ComputeInstanceState.DELETING,
  stopping: ComputeInstanceState.STOPPING,
  stopped: ComputeInstanceState.STOPPED,
  paused: ComputeInstanceState.PAUSED,
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
