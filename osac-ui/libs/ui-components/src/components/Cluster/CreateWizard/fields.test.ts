import { type MessageInitShape, create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import { getInitialFormValues } from './fields';

const clusterCatalogItemInit: MessageInitShape<typeof ClusterCatalogItemSchema> = {
  id: 'catalog-openshift-4',
  metadata: {
    name: 'catalog-openshift-4',
    creator: 'foo',
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'OpenShift 4 cluster',
  description: 'Standard OpenShift cluster offering',
  template: {
    id: 'tpl-openshift-4',
  },
  published: true,
};

const clusterCatalogItem = create(ClusterCatalogItemSchema, clusterCatalogItemInit);

describe('getInitialFormValues — node sets', () => {
  it('prepopulates and locks node set rows from a locked catalog policy', () => {
    const values = getInitialFormValues(
      create(ClusterCatalogItemSchema, {
        ...clusterCatalogItemInit,
        fields: {
          nodeSets: {
            behavior: {
              case: 'locked',
              value: {
                items: {
                  workers: {
                    size: 2,
                    baremetalInstanceType: { id: 'bmit-worker', name: 'worker', shared: true },
                  },
                },
              },
            },
          },
        },
      }),
    );

    expect(values.spec.nodeSetRows.locked).toBe(true);
    expect(values.spec.nodeSetRows.value).toHaveLength(1);
    expect(values.spec.nodeSetRows.value[0]).toMatchObject({
      name: 'workers',
      hostType: 'bmit-worker',
      size: '2',
    });
  });

  it('prepopulates editable node set host types from catalog defaults', () => {
    const values = getInitialFormValues(
      create(ClusterCatalogItemSchema, {
        ...clusterCatalogItemInit,
        fields: {
          nodeSets: {
            behavior: {
              case: 'editable',
              value: {
                defaultValue: {
                  items: {
                    workers: {
                      size: 3,
                      baremetalInstanceType: { id: 'bmit-gpu', name: 'gpu-host' },
                    },
                    infra: {
                      size: 1,
                      baremetalInstanceType: { name: 'worker' },
                    },
                  },
                },
              },
            },
          },
        },
      }),
    );

    expect(values.spec.nodeSetRows.locked).toBe(false);
    expect(values.spec.nodeSetRows.value).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          name: 'workers',
          hostType: 'bmit-gpu',
          size: '3',
        }),
        expect.objectContaining({
          name: 'infra',
          hostType: 'worker',
          size: '1',
        }),
      ]),
    );
  });

  it('keeps an empty unlocked row when the catalog does not govern node sets', () => {
    const values = getInitialFormValues(clusterCatalogItem);

    expect(values.spec.nodeSetRows.locked).toBe(false);
    expect(values.spec.nodeSetRows.value).toHaveLength(1);
    expect(values.spec.nodeSetRows.value[0]).toMatchObject({
      name: '',
      hostType: '',
      size: '',
    });
  });
});

describe('getInitialFormValues — network attachment', () => {
  it('locks the attachment unit without locking nested pickers', () => {
    const values = getInitialFormValues(
      create(ClusterCatalogItemSchema, {
        ...clusterCatalogItemInit,
        fields: {
          networkAttachment: {
            behavior: {
              case: 'locked',
              value: {
                subnet: { id: 'subnet-1', name: 'subnet-a' },
                securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
              },
            },
          },
        },
      }),
    );

    expect(values.spec.networkAttachment).toEqual({
      value: {
        useDefaultNetwork: false,
        virtualNetwork: { id: '', name: '' },
        subnet: { id: 'subnet-1', name: 'subnet-a' },
        securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
      },
      locked: true,
    });
  });
});
