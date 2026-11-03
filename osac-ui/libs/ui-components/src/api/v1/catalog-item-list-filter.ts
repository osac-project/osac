import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import { type CelFilter, cel } from '../cel';

export type CatalogListFilterCriteria = {
  search?: string;
  published?: 'published' | 'unpublished';
  tenant?: string;
};

export type CatalogListResource =
  | BareMetalInstanceCatalogItem
  | ClusterCatalogItem
  | ComputeInstanceCatalogItem;

const GLOBAL_TENANT = 'shared';

export const buildCatalogListFilter = (
  criteria: CatalogListFilterCriteria,
): CelFilter<CatalogListResource> | undefined => {
  const search = criteria.search?.trim();
  const published = criteria.published ? criteria.published === 'published' : undefined;
  const tenant = criteria.tenant?.trim();
  const tenants = tenant ? [GLOBAL_TENANT, tenant] : undefined;
  const hasCriteria = Boolean(search || published !== undefined || tenants);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<CatalogListResource>((filter) => {
    const clauses: CelFilter<CatalogListResource>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('title').contains(search),
        ),
      );
    }

    if (published !== undefined) {
      clauses.push(filter.field('published').equals(published));
    }

    if (tenants) {
      clauses.push(
        filter.or(...tenants.map((tenant) => filter.field('metadata.tenant').equals(tenant))),
      );
    }

    return filter.and(...clauses);
  });
};
