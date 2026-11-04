import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import { getClusterCatalogItemInitialValues } from './values';

describe('getClusterCatalogItemInitialValues', () => {
  it('uses the bare-metal instance-type name for catalog node sets', () => {
    const item = create(ClusterCatalogItemSchema, {
      fields: {
        nodeSets: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: {
                items: {
                  workers: {
                    size: 2,
                    baremetalInstanceType: { id: 'bmit-1', name: 'worker' },
                  },
                },
              },
            },
          },
        },
      },
    });

    expect(getClusterCatalogItemInitialValues(item).nodeSets.value).toEqual([
      expect.objectContaining({
        name: 'workers',
        hostType: { id: 'bmit-1', name: 'worker' },
        size: '2',
      }),
    ]);
  });

  it('does not fall back to the bare-metal instance-type ID', () => {
    const item = create(ClusterCatalogItemSchema, {
      fields: {
        nodeSets: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: {
                items: {
                  workers: {
                    size: 2,
                    baremetalInstanceType: { id: 'bmit-1' },
                  },
                },
              },
            },
          },
        },
      },
    });

    expect(getClusterCatalogItemInitialValues(item).nodeSets.value).toEqual([
      expect.objectContaining({
        name: 'workers',
        hostType: { id: 'bmit-1', name: '' },
        size: '2',
      }),
    ]);
  });
});
