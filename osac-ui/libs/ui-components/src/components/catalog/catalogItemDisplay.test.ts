import { describe, expect, it } from 'vitest';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import { catalogItemDetailsPath, filterCatalogItemsBySearch } from './catalogItemDisplay';
import {
  catalogItemFieldDefinitions,
  readCatalogItemFieldDefinitions,
} from '../catalogProvision/catalogFieldDefinition';

describe('readCatalogItemFieldDefinitions', () => {
  it('reads snake_case field_definitions from wire JSON', () => {
    const wireItem = {
      id: 'catalog-1',
      field_definitions: [
        {
          path: 'cores',
          display_name: 'vCPUs',
          editable: true,
          default: { number_value: 4 },
          validation_schema: '{"type":"integer","minimum":2}',
        },
      ],
      templateParameters: {},
    };

    expect(readCatalogItemFieldDefinitions(wireItem)).toHaveLength(1);
    expect(catalogItemFieldDefinitions(wireItem)).toEqual([
      {
        path: 'cores',
        displayName: 'vCPUs',
        editable: true,
        default: 4,
        validationSchema: { type: 'integer', minimum: 2 },
      },
    ]);
  });

  it('parses post-decode protobuf Value defaults without mutating the catalog item', () => {
    const decodedItem = {
      id: 'catalog-1',
      fieldDefinitions: [
        {
          path: 'cores',
          displayName: 'vCPUs',
          editable: true,
          default: { kind: { case: 'numberValue', value: 4 } },
        },
      ],
      templateParameters: {},
    };

    expect(catalogItemFieldDefinitions(decodedItem)).toEqual([
      {
        path: 'cores',
        displayName: 'vCPUs',
        editable: true,
        default: 4,
      },
    ]);
    expect(decodedItem.fieldDefinitions[0]?.default).toEqual({
      kind: { case: 'numberValue', value: 4 },
    });
  });
});

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
