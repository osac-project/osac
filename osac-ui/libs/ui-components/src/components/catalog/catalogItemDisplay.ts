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

export const selectCatalogItemValue = <TVm, TBm, TCluster>(
  item: CatalogItem,
  values: { vm: TVm; bm: TBm; cluster: TCluster },
): TVm | TBm | TCluster => {
  switch (item.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return values.vm;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return values.bm;
    case 'osac.public.v1.ClusterCatalogItem':
      return values.cluster;
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

  return [item.metadata?.name, ...Object.entries(labels).map(([key, value]) => `${key} ${value}`)]
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

export const catalogItemListPathForKind = (kind: CatalogItemKind): string => {
  switch (kind) {
    case 'vm':
      return '/catalog/vms';
    case 'cluster':
      return '/catalog/clusters';
    case 'bm':
      return '/catalog/bare-metal';
  }
};

export const getCatalogCreateActionPath = (item: CatalogItem): string => {
  const kind = getCatalogItemKind(item);
  return kind ? `${catalogItemListPathForKind(kind)}/${item.id}/launch` : '#';
};

export const catalogItemListPath = (item: CatalogItem): string => {
  const kind = getCatalogItemKind(item);
  return kind ? catalogItemListPathForKind(kind) : '/catalog';
};

export const catalogItemDetailsPath = (item: CatalogItem): string => {
  const listPath = catalogItemListPath(item);
  return listPath === '/catalog' ? '#' : `${listPath}/${item.id}`;
};

export const catalogItemEditPath = (item: CatalogItem): string => {
  const kind = getCatalogItemKind(item);
  return kind ? `/catalog/${kind}/${item.id}/edit` : '#';
};
