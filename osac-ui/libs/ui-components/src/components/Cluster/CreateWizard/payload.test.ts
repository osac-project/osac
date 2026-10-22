import { type MessageInitShape, create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import { createEmptyClusterValues, createEmptyNodeSetRow } from './fields';
import { buildClusterCreatePayload } from './payload';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

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

describe('buildClusterCreatePayload', () => {
  it('builds catalog-item create payload with node sets keyed by user-defined IDs', () => {
    const row = createEmptyNodeSetRow();
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        sshPublicKey: provisioningFieldValue('ssh-rsa AAAA'),
        pullSecretSecret: provisioningFieldValue('pull-secret'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          {
            ...row,
            name: 'production',
            hostType: 'acme_1tb',
            size: '3',
          },
        ]),
        network: {
          podCidr: provisioningFieldValue('10.128.0.0/14'),
          serviceCidr: provisioningFieldValue(''),
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
          production: { baremetalInstanceType: { id: 'acme_1tb' }, size: 3 },
        },
        network: {
          podCidr: '10.128.0.0/14',
        },
        autoExternalIpAttachment: false,
      },
    });
  });

  it('omits blank optional fields, version, and node sets when no valid rows exist', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'empty-pools', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        versionName: provisioningFieldValue(''),
        nodeSetRows: provisioningFieldValue([]),
        network: { podCidr: provisioningFieldValue(''), serviceCidr: provisioningFieldValue('') },
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).toEqual({
      catalogItem: { id: clusterCatalogItem.id },
      pullSecretSecret: { name: 'secret' },
      autoExternalIpAttachment: false,
    });
    expect(payload.spec).not.toHaveProperty('version');
    expect(payload.spec).not.toHaveProperty('nodeSets');
    expect(payload.spec).not.toHaveProperty('network');
  });

  it('filters out invalid node set rows from the payload', () => {
    const row = createEmptyNodeSetRow();
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'filtered-pools', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          { ...row, name: 'empty-host-type', hostType: '', size: '3' },
          { ...row, name: 'zero-size', hostType: 'acme_1tb', size: '0' },
          { ...row, name: 'invalid-size', hostType: 'acme_2tb', size: 'not-a-number' },
          { ...row, name: 'production', hostType: 'acme_1tb', size: '3' },
          { ...row, name: 'development', hostType: 'acme_1tb', size: '2' },
        ]),
        network: { podCidr: provisioningFieldValue(''), serviceCidr: provisioningFieldValue('') },
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec?.nodeSets).toEqual({
      production: { baremetalInstanceType: { id: 'acme_1tb' }, size: 3 },
      development: { baremetalInstanceType: { id: 'acme_1tb' }, size: 2 },
    });
  });

  it('preserves __proto__ as a node set name in the payload', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'proto-pool', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          {
            ...createEmptyNodeSetRow(),
            name: '__proto__',
            hostType: 'acme_1tb',
            size: '3',
          },
        ]),
        network: { podCidr: provisioningFieldValue(''), serviceCidr: provisioningFieldValue('') },
      },
    };

    const nodeSets = buildClusterCreatePayload(values, clusterCatalogItem).spec?.nodeSets;
    expect(Object.keys(nodeSets ?? {})).toContain('__proto__');
    expect(nodeSets?.['__proto__']).toEqual({
      baremetalInstanceType: { id: 'acme_1tb' },
      size: 3,
    });
  });

  it('omits networkAttachment when useDefaultNetwork is true', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: true,
          virtualNetwork: { id: 'vn-1', name: 'vn-1' },
          subnet: { id: 'subnet-1', name: 'subnet-1' },
          securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
        }),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).not.toHaveProperty('networkAttachment');
  });

  it('omits networkAttachment when useDefaultNetwork is false but pickers are empty', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: '', name: '' },
          subnet: { id: '', name: '' },
          securityGroups: [],
        }),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).not.toHaveProperty('networkAttachment');
  });

  it('includes networkAttachment with subnet and security groups when pickers have values', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: 'vn-1', name: 'tenant-vn' },
          subnet: { id: 'my-subnet', name: 'tenant-subnet' },
          securityGroups: [
            { id: 'sg-1', name: 'default-sg-1' },
            { id: 'sg-2', name: 'default-sg-2' },
          ],
        }),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec?.networkAttachment).toEqual({
      subnet: { id: 'my-subnet' },
      securityGroups: [{ id: 'sg-1' }, { id: 'sg-2' }],
    });
  });

  it('includes autoExternalIpAttachment when true', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        autoExternalIpAttachment: provisioningFieldValue(true),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec?.autoExternalIpAttachment).toBe(true);
  });

  it('includes autoExternalIpAttachment when false so catalog defaults are not applied', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        autoExternalIpAttachment: provisioningFieldValue(false),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec?.autoExternalIpAttachment).toBe(false);
  });

  it.each([
    ['default (no project)', ''],
    ['top-level project', 'my-project'],
    ['nested project path', 'parent.child'],
  ])('passes the selected %s through to metadata.project', (_label, project) => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
      },
    };

    expect(buildClusterCreatePayload(values, clusterCatalogItem).metadata).toEqual({
      name: 'my-cluster',
      project,
    });
  });
});

describe('buildClusterCreatePayload — locked catalog fields', () => {
  it('omits a locked version so the catalog policy can apply it', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'locked-version', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        versionName: provisioningFieldValue('4-22-0', true),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).not.toHaveProperty('version');
    expect(payload.spec?.pullSecretSecret).toEqual({ name: 'secret' });
  });

  it('sends version when the catalog policy is not locked', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'editable-version', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        versionName: provisioningFieldValue('4-20-0'),
      },
    };

    expect(buildClusterCreatePayload(values, clusterCatalogItem).spec?.version).toEqual({
      name: '4-20-0',
    });
  });

  it('omits locked node sets, CIDRs, SSH key, and auto external IP', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'locked-fields', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('secret'),
        sshPublicKey: provisioningFieldValue('ssh-ed25519 AAAA', true),
        nodeSetRows: provisioningFieldValue(
          [
            {
              ...createEmptyNodeSetRow(),
              name: 'workers',
              hostType: 'bmit-worker',
              size: '2',
            },
          ],
          true,
        ),
        network: {
          podCidr: provisioningFieldValue('10.128.0.0/14', true),
          serviceCidr: provisioningFieldValue('172.30.0.0/16', true),
        },
        autoExternalIpAttachment: provisioningFieldValue(true, true),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).not.toHaveProperty('sshPublicKey');
    expect(payload.spec).not.toHaveProperty('nodeSets');
    expect(payload.spec).not.toHaveProperty('network');
    expect(payload.spec).not.toHaveProperty('autoExternalIpAttachment');
  });

  it('omits a locked pull secret and custom network attachment', () => {
    const values = {
      ...createEmptyClusterValues(),
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'locked-attachment', project: '' },
      spec: {
        ...createEmptyClusterValues().spec,
        pullSecretSecret: provisioningFieldValue('catalog-secret', true),
        networkAttachment: provisioningFieldValue(
          {
            useDefaultNetwork: false,
            virtualNetwork: { id: 'vn-1', name: 'vn-1' },
            subnet: { id: 'subnet-1', name: 'subnet-1' },
            securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
          },
          true,
        ),
      },
    };

    const payload = buildClusterCreatePayload(values, clusterCatalogItem);
    expect(payload.spec).not.toHaveProperty('pullSecretSecret');
    expect(payload.spec).not.toHaveProperty('networkAttachment');
  });
});
