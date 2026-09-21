import { describe, expect, it } from 'vitest';

import type { ClusterCatalogItem } from '@osac/types';

import { createEmptyNodeSetRow } from './fields';
import { buildClusterCreatePayload, createEmptyClusterValues } from './payload';

const clusterCatalogItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-openshift-4',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-openshift-4',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'OpenShift 4 cluster',
  description: 'Standard OpenShift cluster offering',
  template: {
    $typeName: 'osac.public.v1.ClusterTemplateReference',
    id: 'tpl-openshift-4',
    name: '',
    project: '',
    shared: false,
  },
  published: true,
  templateParameters: {},
};

describe('buildClusterCreatePayload', () => {
  it('builds catalog-item create payload with node sets keyed by user-defined IDs', () => {
    const row = createEmptyNodeSetRow();
    const values = {
      ...createEmptyClusterValues(),
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        sshPublicKey: 'ssh-rsa AAAA',
        pullSecretSecret: { name: 'pull-secret' },
        versionName: '4-17-0',
        nodeSetRows: [
          {
            ...row,
            name: 'production',
            hostType: 'acme_1tb',
            size: '3',
          },
        ],
        network: {
          podCidr: '10.128.0.0/14',
          serviceCidr: '',
        },
      },
    };

    expect(buildClusterCreatePayload(values, clusterCatalogItem)).toEqual({
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        catalogItem: { id: clusterCatalogItem.id },
        sshPublicKey: 'ssh-rsa AAAA',
        pullSecretSecret: { name: 'pull-secret' },
        version: { name: '4-17-0' },
        nodeSets: {
          production: { hostType: { id: 'acme_1tb' }, size: 3 },
        },
        network: {
          podCidr: '10.128.0.0/14',
        },
      },
    });
  });

  it('omits blank optional fields, version, and node sets when no valid rows exist', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'empty-pools', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: { name: 'secret' },
        versionName: '',
        nodeSetRows: [],
        network: { podCidr: '', serviceCidr: '' },
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).toEqual({
      catalogItem: { id: clusterCatalogItem.id },
      pullSecretSecret: { name: 'secret' },
    });
    expect(payload.spec).not.toHaveProperty('version');
    expect(payload.spec).not.toHaveProperty('nodeSets');
    expect(payload.spec).not.toHaveProperty('network');
  });

  it('filters out invalid node set rows from the payload', () => {
    const row = createEmptyNodeSetRow();
    const values = {
      ...createEmptyClusterValues(),
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'filtered-pools', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: { name: 'secret' },
        versionName: '4-17-0',
        nodeSetRows: [
          { ...row, name: 'empty-host-type', hostType: '', size: '3' },
          { ...row, name: 'zero-size', hostType: 'acme_1tb', size: '0' },
          { ...row, name: 'invalid-size', hostType: 'acme_2tb', size: 'not-a-number' },
          { ...row, name: 'production', hostType: 'acme_1tb', size: '3' },
          { ...row, name: 'development', hostType: 'acme_1tb', size: '2' },
        ],
        network: { podCidr: '', serviceCidr: '' },
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec?.nodeSets).toEqual({
      production: { hostType: { id: 'acme_1tb' }, size: 3 },
      development: { hostType: { id: 'acme_1tb' }, size: 2 },
    });
  });

  it('preserves __proto__ as a node set name in the payload', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'proto-pool', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: { name: 'secret' },
        versionName: '4-17-0',
        nodeSetRows: [
          { ...createEmptyNodeSetRow(), name: '__proto__', hostType: 'acme_1tb', size: '3' },
        ],
        network: { podCidr: '', serviceCidr: '' },
      },
    };

    const nodeSets = buildClusterCreatePayload(values, clusterCatalogItem).spec?.nodeSets;
    expect(Object.keys(nodeSets ?? {})).toContain('__proto__');
    expect(nodeSets?.['__proto__']).toEqual({ hostType: { id: 'acme_1tb' }, size: 3 });
  });

  it.each([
    ['default (no project)', ''],
    ['top-level project', 'my-project'],
    ['nested project path', 'parent.child'],
  ])('passes the selected %s through to metadata.project', (_label, project) => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'my-cluster', project },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: { name: 'secret' },
      },
    };

    expect(buildClusterCreatePayload(values, clusterCatalogItem).metadata).toEqual({
      name: 'my-cluster',
      project,
    });
  });
});
