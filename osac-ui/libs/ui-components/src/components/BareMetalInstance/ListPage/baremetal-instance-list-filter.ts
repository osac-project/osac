import type { BareMetalInstance } from '@osac/types';
import { BareMetalInstanceState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

import { type BareMetalPowerStateFilter } from '../bareMetalInstanceDisplay';

const POWER_STATE_TO_INSTANCE_STATE: Record<BareMetalPowerStateFilter, BareMetalInstanceState> = {
  running: BareMetalInstanceState.RUNNING,
  stopped: BareMetalInstanceState.STOPPED,
  provisioning: BareMetalInstanceState.PROVISIONING,
  starting: BareMetalInstanceState.STARTING,
  failing: BareMetalInstanceState.FAILED,
};

export type BareMetalListFilterCriteria = {
  search?: string;
  powerState?: BareMetalPowerStateFilter;
  instanceType?: string;
  diskImage?: string;
};

export const buildBareMetalListFilter = (
  criteria: BareMetalListFilterCriteria,
): CelFilter<BareMetalInstance> | undefined => {
  const search = criteria.search?.trim();
  const hasCriteria = Boolean(
    search || criteria.powerState || criteria.instanceType || criteria.diskImage,
  );

  if (!hasCriteria) {
    return undefined;
  }

  return cel<BareMetalInstance>((filter) => {
    const clauses: CelFilter<BareMetalInstance>[] = [];

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

    if (criteria.instanceType) {
      clauses.push(filter.field('spec.instanceType.name').equals(criteria.instanceType));
    }

    if (criteria.diskImage) {
      clauses.push(filter.field('spec.diskImage.name').equals(criteria.diskImage));
    }

    return filter.and(...clauses);
  });
};
