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
  published?: boolean;
};

const GLOBAL_TENANT = 'shared';

interface CatalogListFilterTerms {
  search?: string;
  published?: boolean;
  tenants?: string[];
}

const filterTermsFromCriteria = (criteria: CatalogListFilterCriteria): CatalogListFilterTerms => {
  const search = criteria.search?.trim();
  const tenant = criteria.tenant?.trim();

  return {
    ...(search ? { search } : {}),
    ...(criteria.published ? { published: criteria.published === 'published' } : {}),
    ...(tenant ? { tenants: [GLOBAL_TENANT, tenant] } : {}),
  };
};

const matchesFilterTerms = (
  item: CatalogListFilterableItem,
  terms: CatalogListFilterTerms,
): boolean => {
  if (terms.published !== undefined && item.published !== terms.published) {
    return false;
  }

  if (terms.tenants && !terms.tenants.includes(item.metadata?.tenant ?? '')) {
    return false;
  }

  const search = terms.search;
  if (search && ![item.metadata?.name, item.title].some((value) => value?.includes(search))) {
    return false;
  }

  return true;
};

const unescapeCelStringLiteral = (value: string): string =>
  value.replaceAll('\\"', '"').replaceAll('\\\\', '\\');

const filterTermsFromCel = (filter: string | undefined): CatalogListFilterTerms => {
  if (!filter) {
    return {};
  }

  const publishedMatch = filter.match(/this\.published == (true|false)/);
  const tenants = [...filter.matchAll(/this\.metadata\.tenant == "((?:\\.|[^"\\])*)"/g)].map(
    (match) => unescapeCelStringLiteral(match[1]),
  );
  const searchMatch = filter.match(/this\.metadata\.name\.contains\("((?:\\.|[^"\\])*)"\)/);

  return {
    ...(publishedMatch ? { published: publishedMatch[1] === 'true' } : {}),
    ...(tenants.length ? { tenants } : {}),
    ...(searchMatch ? { search: unescapeCelStringLiteral(searchMatch[1]) } : {}),
  };
};

/** Evaluates the catalog filter syntax used by the mock list handlers. */
export const catalogItemMatchesListFilter = (
  item: CatalogListFilterableItem,
  filter: string | undefined,
): boolean => matchesFilterTerms(item, filterTermsFromCel(filter));

export const buildCatalogListFilter = (
  criteria: CatalogListFilterCriteria,
): CelFilter<CatalogListResource> | undefined => {
  const terms = filterTermsFromCriteria(criteria);
  const hasCriteria = Boolean(terms.search || terms.published !== undefined || terms.tenants);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<CatalogListResource>((filter) => {
    const clauses: CelFilter<CatalogListResource>[] = [];

    if (terms.search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(terms.search),
          filter.field('title').contains(terms.search),
        ),
      );
    }

    if (terms.published !== undefined) {
      clauses.push(filter.field('published').equals(terms.published));
    }

    if (terms.tenants) {
      clauses.push(
        filter.or(...terms.tenants.map((tenant) => filter.field('metadata.tenant').equals(tenant))),
      );
    }

    return filter.and(...clauses);
  });
};
