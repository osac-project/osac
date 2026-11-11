import type { TFunction } from 'i18next';

import { InstanceTypeState, type InstanceType as PrivateInstanceType } from '@osac/types/private';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const ADMIN_INSTANCE_TYPE_LIFECYCLE_FILTERS = ['active', 'deprecated', 'obsolete'] as const;

export type AdminInstanceTypeLifecycleFilter =
  (typeof ADMIN_INSTANCE_TYPE_LIFECYCLE_FILTERS)[number];

const LIFECYCLE_FILTER_TO_STATE: Record<AdminInstanceTypeLifecycleFilter, InstanceTypeState> = {
  active: InstanceTypeState.ACTIVE,
  deprecated: InstanceTypeState.DEPRECATED,
  obsolete: InstanceTypeState.OBSOLETE,
};

export const isAdminInstanceTypeLifecycleFilter = (
  value: string,
): value is AdminInstanceTypeLifecycleFilter =>
  (ADMIN_INSTANCE_TYPE_LIFECYCLE_FILTERS as readonly string[]).includes(value);

export const adminInstanceTypeLifecycleFilterLabel = (
  filter: AdminInstanceTypeLifecycleFilter,
  t: TFunction,
): string => {
  switch (filter) {
    case 'active':
      return t('Active');
    case 'deprecated':
      return t('Deprecated');
    case 'obsolete':
      return t('Obsolete');
  }
};

export type AdminInstanceTypeListFilterCriteria = {
  search?: string;
  lifecycles?: readonly AdminInstanceTypeLifecycleFilter[];
};

export const buildAdminInstanceTypeListFilter = (
  criteria: AdminInstanceTypeListFilterCriteria,
): CelFilter<PrivateInstanceType> | undefined => {
  const search = criteria.search?.trim();
  const lifecycles = criteria.lifecycles ?? [];
  const hasCriteria = Boolean(search || lifecycles.length);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<PrivateInstanceType>((filter) => {
    const clauses: CelFilter<PrivateInstanceType>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('id').contains(search),
        ),
      );
    }

    if (lifecycles.length > 0) {
      clauses.push(
        filter
          .field('spec.state')
          .isIn(lifecycles.map((lifecycle) => LIFECYCLE_FILTER_TO_STATE[lifecycle])),
      );
    }

    return filter.and(...clauses);
  });
};
