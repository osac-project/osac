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

export type CatalogListFilterableItem = {
  metadata?: { name?: string; tenant?: string };
  title?: string;
  description?: string;
  published?: boolean;
};

const GLOBAL_TENANT = 'shared';

/** Mirrors {@link buildCatalogListFilter} for unit tests and mock list handlers. */
export const catalogItemMatchesListFilter = (
  item: CatalogListFilterableItem,
  filter: string | undefined,
): boolean => {
  if (!filter) {
    return true;
  }

  if (filter.includes('this.published == true') && !item.published) {
    return false;
  }
  if (filter.includes('this.published == false') && item.published) {
    return false;
  }

  const tenantEqualsMatches = [
    ...filter.matchAll(/this\.metadata\.tenant == "((?:\\.|[^"\\])*)"/g),
  ].map((match) => match[1].replaceAll('\\"', '"').replaceAll('\\\\', '\\'));
  if (tenantEqualsMatches.length > 0) {
    const tenant = item.metadata?.tenant;
    if (!tenant || !tenantEqualsMatches.includes(tenant)) {
      return false;
    }
  }

  const containsMatches = [
    ...filter.matchAll(
      /this\.(?:metadata\.name|title|description)\.contains\("((?:\\.|[^"\\])*)"\)/g,
    ),
  ].map((match) => match[1].replaceAll('\\"', '"').replaceAll('\\\\', '\\'));
  if (containsMatches.length > 0) {
    const haystack = [item.metadata?.name, item.title, item.description].filter(Boolean).join(' ');
    if (!containsMatches.some((term) => haystack.includes(term))) {
      return false;
    }
  }

  return true;
};

export const buildCatalogListFilter = (
  criteria: CatalogListFilterCriteria,
): CelFilter<CatalogListResource> | undefined => {
  const hasCriteria = Boolean(
    criteria.search?.trim() || criteria.published || criteria.tenant?.trim(),
  );

  if (!hasCriteria) {
    return undefined;
  }

  return cel<CatalogListResource>((filter) => {
    const clauses: CelFilter<CatalogListResource>[] = [];

    const search = criteria.search?.trim();
    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('title').contains(search),
          filter.field('description').contains(search),
        ),
      );
    }

    if (criteria.published) {
      clauses.push(filter.field('published').equals(criteria.published === 'published'));
    }

    const tenant = criteria.tenant?.trim();
    if (tenant) {
      clauses.push(
        filter.or(
          filter.field('metadata.tenant').equals(GLOBAL_TENANT),
          filter.field('metadata.tenant').equals(tenant),
        ),
      );
    }

    return filter.and(...clauses);
  });
};
