import type { BareMetalInstanceType as PrivateBareMetalInstanceType } from '@osac/types/private';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export type AdminBareMetalInstanceTypeListFilterCriteria = {
  search?: string;
};

export const buildAdminBareMetalInstanceTypeListFilter = (
  criteria: AdminBareMetalInstanceTypeListFilterCriteria,
): CelFilter<PrivateBareMetalInstanceType> | undefined => {
  const search = criteria.search?.trim();

  if (!search) {
    return undefined;
  }

  return cel<PrivateBareMetalInstanceType>((filter) =>
    filter.or(filter.field('metadata.name').contains(search), filter.field('id').contains(search)),
  );
};
