import type { TFunction } from 'i18next';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import {
  CATALOG_ITEM_RESOURCE_FIELD_PATHS,
  type CatalogFieldDefinition,
  type CatalogItemResourceFieldPath,
  catalogItemFieldDefinitions,
  fieldDefinitionDefaultToInputString,
  isCatalogCardResourceFieldPath,
  isCatalogItemResourceFieldPath,
  isClusterCatalogItemResourceFieldPath,
  normalizeCatalogFieldPath,
  resolvedFieldDefault,
} from '../catalogProvision/catalogFieldDefinition';

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

const FALLBACK_RESOURCE_LABELS: Record<CatalogItemResourceFieldPath, string> = {
  cores: 'vCPUs',
  memory_gib: 'Memory',
  'boot_disk.size_gib': 'Boot disk',
};

/** Field definitions shown as resource labels on catalog cards (VM or cluster). */
export const catalogItemResourceFieldDefinitions = (
  item: CatalogItem,
): CatalogFieldDefinition[] => {
  const defs = catalogItemFieldDefinitions(item);
  const byNormalizedPath = new Map(defs.map((def) => [normalizeCatalogFieldPath(def.path), def]));

  const vmResourceDefs = CATALOG_ITEM_RESOURCE_FIELD_PATHS.flatMap((path) => {
    const def = byNormalizedPath.get(path);
    return def ? [def] : [];
  });
  if (vmResourceDefs.length > 0) {
    return vmResourceDefs;
  }

  return defs.filter((def) => isClusterCatalogItemResourceFieldPath(def.path));
};

/** Configuration defaults shown on the detail page (excludes resource chips already listed above). */
export const catalogItemConfigurationFieldDefinitions = (
  item: CatalogItem,
): CatalogFieldDefinition[] => {
  return catalogItemFieldDefinitions(item).filter(
    (def) => !isCatalogCardResourceFieldPath(def.path),
  );
};

export const formatCatalogResourcePart = (def: CatalogFieldDefinition): string | null => {
  if (!isCatalogCardResourceFieldPath(def.path)) {
    return null;
  }
  const defaultValue = resolvedFieldDefault(def);
  if (defaultValue === undefined || defaultValue === null) {
    return null;
  }
  const value = fieldDefinitionDefaultToInputString(defaultValue).trim();
  if (!value) {
    return null;
  }
  const normalizedPath = normalizeCatalogFieldPath(def.path);
  const label = isCatalogItemResourceFieldPath(def.path)
    ? def.displayName || FALLBACK_RESOURCE_LABELS[normalizedPath as CatalogItemResourceFieldPath]
    : def.displayName;
  if (!label) {
    return null;
  }
  return `${value} ${label}`;
};

export const catalogItemResourceParts = (item: CatalogItem): string[] => {
  return catalogItemResourceFieldDefinitions(item)
    .map((def) => formatCatalogResourcePart(def))
    .filter((part): part is string => part != null);
};

export const catalogItemResourceLine = (item: CatalogItem): string | undefined => {
  const parts = catalogItemResourceParts(item);
  return parts.length ? parts.join(' · ') : undefined;
};

export const searchableCatalogItemText = (item: CatalogItem): string => {
  const labels = item.metadata?.labels ?? {};
  const fieldText = catalogItemFieldDefinitions(item)
    .map(
      (def) =>
        `${def.displayName} ${fieldDefinitionDefaultToInputString(resolvedFieldDefault(def))}`,
    )
    .join(' ');

  return [
    item.description,
    item.metadata?.name,
    fieldText,
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

export const formatCatalogFieldDefault = (def: CatalogFieldDefinition): string => {
  const defaultValue = resolvedFieldDefault(def);
  if (defaultValue === undefined) {
    return '—';
  }
  return fieldDefinitionDefaultToInputString(defaultValue) || '—';
};

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
