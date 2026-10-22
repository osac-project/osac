import type { TFunction } from 'i18next';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

export const isCatalogItemKind = (value: string | undefined): value is CatalogItemKind =>
  value === 'vm' || value === 'cluster' || value === 'bm';

export type CatalogItemKind = 'vm' | 'bm' | 'cluster';

export type CatalogItem =
  | ClusterCatalogItem
  | BareMetalInstanceCatalogItem
  | ComputeInstanceCatalogItem;

export const catalogItemTypeBadgeLabel = (kind: CatalogItem, t: TFunction): string => {
  switch (kind.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return t('Virtual Machine');
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return t('Bare Metal');
    case 'osac.public.v1.ClusterCatalogItem':
      return t('Cluster');
    default:
      return t('Unknown');
  }
};

export const getCatalogItemKind = (kind: CatalogItem): CatalogItemKind | undefined => {
  switch (kind.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return 'vm';
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return 'bm';
    case 'osac.public.v1.ClusterCatalogItem':
      return 'cluster';
    default:
      return undefined;
  }
};

export const catalogItemMetadataLabelEntries = (
  item: CatalogItem,
): Array<{ key: string; value: string }> => {
  const labels = item.metadata?.labels;
  if (!labels) {
    return [];
  }
  return Object.entries(labels)
    .map(([key, value]) => ({ key, value: value.trim() }))
    .filter(({ value }) => value.length > 0)
    .sort((a, b) => a.key.localeCompare(b.key));
};

export const searchableCatalogItemText = (item: CatalogItem): string => {
  const labels = item.metadata?.labels ?? {};

  return [
    item.description,
    item.metadata?.name,
    ...Object.entries(labels).map(([key, value]) => `${key} ${value}`),
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase();
};

export const filterCatalogItemsBySearch = (items: CatalogItem[], search: string): CatalogItem[] => {
  const searchTerm = search.trim().toLowerCase();
  if (!searchTerm) {
    return items;
  }
  return items.filter((item) => searchableCatalogItemText(item).includes(searchTerm));
};

export type CatalogPublishedFilter = 'published' | 'unpublished';

export const isCatalogPublishedFilter = (value: string): value is CatalogPublishedFilter =>
  value === 'published' || value === 'unpublished';

export const GLOBAL_TENANT_VALUE = 'shared';

export const getCatalogCreateActionPath = (item: CatalogItem) => {
  switch (item.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return `/vms/create/${item.id}`;
    case 'osac.public.v1.ClusterCatalogItem':
      return `/clusters/create/${item.id}`;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return `/bare-metal/create/${item.id}`;
    default:
      return '#';
  }
};

export const catalogItemDetailsPath = (item: CatalogItem): string => {
  switch (item.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return `/catalog/vm/${item.id}`;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return `/catalog/bm/${item.id}`;
    case 'osac.public.v1.ClusterCatalogItem':
      return `/catalog/cluster/${item.id}`;
    default:
      return '#';
  }
};
