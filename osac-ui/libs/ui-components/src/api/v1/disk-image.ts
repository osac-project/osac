import { Architecture, type DiskImage, DiskImageLifecycle, GuestOSFamily } from '@osac/types';

import { type CelFilter, cel } from '../cel';

export const DISK_IMAGE_NON_OBSOLETE_FILTER = cel<DiskImage>((filter) =>
  filter.field('spec.lifecycle').notEquals(DiskImageLifecycle.OBSOLETE),
);

const shouldApplyDefaultNonObsoleteExclusion = (criteria: DiskImageListFilterCriteria): boolean =>
  !criteria.showObsolete && !criteria.lifecycle?.length;

export type DiskImageListFilterCriteria = {
  search?: string;
  guestOsFamily?: GuestOSFamily;
  architecture?: Architecture[];
  lifecycle?: DiskImageLifecycle[];
  showObsolete?: boolean;
  scope?: 'global' | 'tenant';
};

const ALL_DISK_IMAGE_LIFECYCLE_VALUES = [
  DiskImageLifecycle.UNSPECIFIED,
  DiskImageLifecycle.AVAILABLE,
  DiskImageLifecycle.DEPRECATED,
  DiskImageLifecycle.OBSOLETE,
];

const lifecycleEqualsClause = (values: DiskImageLifecycle[]): CelFilter<DiskImage> | undefined =>
  cel<DiskImage>((filter) =>
    values.length === 1
      ? filter.field('spec.lifecycle').equals(values[0])
      : filter.or(...values.map((value) => filter.field('spec.lifecycle').equals(value))),
  );

export const buildDiskImageListFilter = (
  criteria: DiskImageListFilterCriteria,
): CelFilter<DiskImage> | undefined => {
  const hasCriteria = Boolean(
    criteria.search ||
    criteria.guestOsFamily !== undefined ||
    criteria.architecture?.length ||
    criteria.scope ||
    criteria.showObsolete ||
    criteria.lifecycle?.length,
  );

  if (!hasCriteria) {
    return undefined;
  }

  return cel<DiskImage>((filter) => {
    const clauses: CelFilter<DiskImage>[] = [];

    if (criteria.search) {
      clauses.push(filter.field('metadata.name').contains(criteria.search));
    }
    if (criteria.guestOsFamily !== undefined) {
      clauses.push(filter.field('spec.guestOsFamily').equals(criteria.guestOsFamily));
    }
    if (criteria.architecture?.length) {
      clauses.push(filter.field('spec.architecture').someEqualsAny(criteria.architecture, 'a'));
    }
    if (criteria.scope) {
      clauses.push(
        criteria.scope === 'global'
          ? filter.field('metadata.tenant').equals('shared')
          : filter.field('metadata.tenant').notEquals('shared'),
      );
    }

    const selectedLifecycle = criteria.lifecycle ?? [];
    if (criteria.showObsolete) {
      const lifecycleValues = selectedLifecycle.length
        ? [...selectedLifecycle, DiskImageLifecycle.OBSOLETE]
        : ALL_DISK_IMAGE_LIFECYCLE_VALUES;

      const clause = lifecycleEqualsClause(lifecycleValues);
      if (clause) {
        clauses.push(clause);
      }
    } else if (selectedLifecycle.length) {
      const clause = lifecycleEqualsClause(selectedLifecycle);
      if (clause) {
        clauses.push(clause);
      }
    }

    return filter.and(...clauses);
  });
};

export const diskImageListRequestFilter = (
  criteria: DiskImageListFilterCriteria,
): CelFilter<DiskImage> | undefined => {
  const filter = buildDiskImageListFilter(criteria);
  if (!shouldApplyDefaultNonObsoleteExclusion(criteria)) {
    return filter;
  }
  return filter
    ? cel<DiskImage>((builder) =>
        builder.and(builder.group(filter), DISK_IMAGE_NON_OBSOLETE_FILTER),
      )
    : DISK_IMAGE_NON_OBSOLETE_FILTER;
};
