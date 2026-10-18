import { describe, expect, it } from 'vitest';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import {
  catalogItemDetailsPath,
  catalogItemEditPath,
  catalogItemKind,
  filterCatalogItemsBySearch,
} from './catalogItemDisplay';
describe('filterCatalogItemsBySearch', () => {
  const items: ClusterCatalogItem[] = [
    {
      $typeName: 'osac.public.v1.ClusterCatalogItem',
      id: '1',
      title: 'Alpha VM',
      description: 'For testing',
      templateParameters: {},
      published: true,
      template: undefined,
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'alpha-vm',
        creator: '',
        annotations: {},
        labels: {},
        project: '',
        tenant: '',
        version: 1,
      },
    },
    {
      $typeName: 'osac.public.v1.ClusterCatalogItem',
      id: '2',
      title: 'Beta Cluster',
      description: 'Production workload',
      templateParameters: {},
      published: true,
      template: undefined,
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'beta-cluster',
        creator: '',
        annotations: {},
        labels: {},
        project: '',
        tenant: '',
        version: 1,
      },
    },
  ];

  it('returns all items when search is empty or whitespace', () => {
    expect(filterCatalogItemsBySearch(items, '')).toEqual(items);
    expect(filterCatalogItemsBySearch(items, '   ')).toEqual(items);
  });
});

describe('catalogItemDetailsPath', () => {
  it('builds the details path for each catalog item type', () => {
    expect(
      catalogItemDetailsPath({
        $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
        id: 'vm-1',
      } as ComputeInstanceCatalogItem),
    ).toBe('/catalog/vm/vm-1');
    expect(
      catalogItemDetailsPath({
        $typeName: 'osac.public.v1.BareMetalInstanceCatalogItem',
        id: 'bm-1',
      } as BareMetalInstanceCatalogItem),
    ).toBe('/catalog/bm/bm-1');
    expect(
      catalogItemDetailsPath({
        $typeName: 'osac.public.v1.ClusterCatalogItem',
        id: 'cluster-1',
      } as ClusterCatalogItem),
    ).toBe('/catalog/cluster/cluster-1');
  });
});

describe('catalogItemKind', () => {
  it('resolves the kind for each catalog item type', () => {
    expect(
      catalogItemKind({
        $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
        id: 'vm-1',
      } as ComputeInstanceCatalogItem),
    ).toBe('vm');
    expect(
      catalogItemKind({
        $typeName: 'osac.public.v1.BareMetalInstanceCatalogItem',
        id: 'bm-1',
      } as BareMetalInstanceCatalogItem),
    ).toBe('bm');
    expect(
      catalogItemKind({
        $typeName: 'osac.public.v1.ClusterCatalogItem',
        id: 'cluster-1',
      } as ClusterCatalogItem),
    ).toBe('cluster');
  });
});

describe('catalogItemEditPath', () => {
  it('builds the edit path for each catalog item type', () => {
    expect(
      catalogItemEditPath({
        $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
        id: 'vm-1',
      } as ComputeInstanceCatalogItem),
    ).toBe('/catalog/vm/vm-1/edit');
    expect(
      catalogItemEditPath({
        $typeName: 'osac.public.v1.BareMetalInstanceCatalogItem',
        id: 'bm-1',
      } as BareMetalInstanceCatalogItem),
    ).toBe('/catalog/bm/bm-1/edit');
    expect(
      catalogItemEditPath({
        $typeName: 'osac.public.v1.ClusterCatalogItem',
        id: 'cluster-1',
      } as ClusterCatalogItem),
    ).toBe('/catalog/cluster/cluster-1/edit');
  });
});
