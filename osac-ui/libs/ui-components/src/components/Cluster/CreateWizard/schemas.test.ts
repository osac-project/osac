import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import type { ClusterCatalogItem } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import { createEmptyNodeSetRow } from './fields';
import { buildClusterStepSchema } from './schemas';
import { tIdentity as t } from '../../../test-utils/i18n';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

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

const emptyValues: ClusterWizardValues = {
  catalogItemReference: { name: '', id: '' },
  metadata: { name: '', project: '' },
  spec: {
    sshPublicKey: provisioningFieldValue(''),
    pullSecretSecret: provisioningFieldValue(''),
    versionName: provisioningFieldValue(''),
    nodeSetRows: provisioningFieldValue([]),
    network: {
      podCidr: provisioningFieldValue(''),
      serviceCidr: provisioningFieldValue(''),
    },
    networkAttachment: provisioningFieldValue({
      useDefaultNetwork: true,
      virtualNetwork: emptyResourceSelectValue(),
      subnet: emptyResourceSelectValue(),
      securityGroups: [],
    }),
    autoExternalIpAttachment: provisioningFieldValue(false),
  },
};

const validateStep = async (
  stepId: Parameters<typeof buildClusterStepSchema>[0],
  values: ClusterWizardValues,
) => {
  const schema = buildClusterStepSchema(stepId, t);
  if (!schema) {
    return {};
  }
  try {
    await schema.validate(values, { abortEarly: false });
    return {};
  } catch (error) {
    if (!(error instanceof ValidationError)) {
      throw error;
    }
    const errors: Record<string, unknown> = {};
    for (const inner of error.inner.length > 0 ? error.inner : [error]) {
      if (!inner.path) {
        continue;
      }
      const parts = inner.path.split('.');
      let current: Record<string, unknown> = errors;
      for (let index = 0; index < parts.length - 1; index += 1) {
        const key = parts[index];
        if (!current[key] || typeof current[key] !== 'object') {
          current[key] = {};
        }
        current = current[key] as Record<string, unknown>;
      }
      current[parts[parts.length - 1]] = inner.message;
    }
    return errors;
  }
};

describe('buildClusterStepSchema', () => {
  it('requires catalog item on catalog step', async () => {
    const errors = await validateStep('catalog', emptyValues);
    expect(errors).toEqual({
      catalogItemReference: {
        name: 'Select a catalog item',
        id: 'Select a catalog item',
      },
    });
  });

  it('rejects invalid DNS label names on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'MyCluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
      },
    });
    expect(errors).toEqual({
      metadata: {
        name: 'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
      },
    });
  });

  it('rejects missing pull secret on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue(''),
      },
    });
    expect(errors).toEqual({
      spec: {
        pullSecretSecret: {
          value: 'Pull secret is required',
        },
      },
    });
  });

  it('rejects malformed ssh public key on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        sshPublicKey: provisioningFieldValue('not-a-key'),
      },
    });
    expect(errors).toEqual({
      spec: {
        sshPublicKey: {
          value:
            'SSH public key must be in the form "[TYPE] key [[EMAIL]]". Supported types are ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
        },
      },
    });
  });

  it('requires pull secret on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
    });
    expect(errors).toEqual({
      spec: { pullSecretSecret: { value: 'Pull secret is required' } },
    });
  });

  it('requires a version on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue(''),
        nodeSetRows: provisioningFieldValue([
          { ...row, name: 'workers', hostType: 'acme_1tb', size: '3' },
        ]),
      },
    });
    expect(errors).toEqual({
      spec: { versionName: { value: 'Version is required' } },
    });
  });

  it('requires at least one node set on configuration step', async () => {
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([]),
      },
    });
    expect(errors).toEqual({
      spec: { nodeSetRows: { value: 'At least one node set is required' } },
    });
  });

  it('requires positive pool sizes on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          {
            ...row,
            name: 'workers',
            hostType: 'acme_1tb',
            size: '0',
          },
        ]),
      },
    });
    expect(errors).toEqual({
      spec: {
        nodeSetRows: { 'value[0]': { size: 'Pool size must be greater than zero' } },
      },
    });
  });

  it('allows duplicate host types when node set IDs are distinct', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          {
            ...row,
            rowId: 'row-1',
            name: 'production',
            hostType: 'acme_1tb',
            size: '3',
          },
          {
            ...row,
            rowId: 'row-2',
            name: 'development',
            hostType: 'acme_1tb',
            size: '2',
          },
        ]),
      },
    });
    expect(errors).toEqual({});
  });

  it('rejects duplicate node set IDs on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          {
            ...row,
            rowId: 'row-1',
            name: 'production',
            hostType: 'acme_1tb',
            size: '3',
          },
          {
            ...row,
            rowId: 'row-2',
            name: 'production',
            hostType: 'acme_1tb',
            size: '2',
          },
        ]),
      },
    });

    expect(errors).toEqual({
      spec: {
        nodeSetRows: { 'value[1]': { name: 'Node set names must be unique' } },
      },
    });
  });

  it('rejects empty node set IDs on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          { ...row, name: '', hostType: 'acme_1tb', size: '3' },
        ]),
      },
    });

    expect(errors).toEqual({
      spec: {
        nodeSetRows: { 'value[0]': { name: 'Name is required' } },
      },
    });
  });

  it('rejects invalid node set names on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: provisioningFieldValue('foo'),
        versionName: provisioningFieldValue('4-17-0'),
        nodeSetRows: provisioningFieldValue([
          { ...row, name: 'Workers_1', hostType: 'acme_1tb', size: '3' },
        ]),
      },
    });

    expect(errors).toEqual({
      spec: {
        nodeSetRows: {
          'value[0]': {
            name: 'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
          },
        },
      },
    });
  });

  it('validates CIDR format on networking step when values are present', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        network: {
          podCidr: provisioningFieldValue('invalid'),
          serviceCidr: provisioningFieldValue(''),
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        network: {
          podCidr: { value: 'Invalid IPv4 CIDR notation' },
        },
      },
    });
  });

  it('rejects IPv6 pod and service CIDRs on networking step', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        network: {
          podCidr: provisioningFieldValue('fd01::/48'),
          serviceCidr: provisioningFieldValue('fd02::/112'),
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        network: {
          podCidr: { value: 'Invalid IPv4 CIDR notation' },
          serviceCidr: { value: 'Invalid IPv4 CIDR notation' },
        },
      },
    });
  });

  it('rejects overlapping pod and service CIDRs on networking step', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        network: {
          podCidr: provisioningFieldValue('10.128.0.0/14'),
          serviceCidr: provisioningFieldValue('10.128.0.0/14'),
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        network: {
          serviceCidr: { value: 'Service CIDR must not overlap the pod CIDR.' },
        },
      },
    });
  });

  it('validates networking step when useDefaultNetwork is true', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: true,
          virtualNetwork: emptyResourceSelectValue(),
          subnet: emptyResourceSelectValue(),
          securityGroups: [],
        }),
      },
    });
    expect(errors).toEqual({});
  });

  it('requires virtual network and subnet when useDefaultNetwork is false', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: emptyResourceSelectValue(),
          subnet: emptyResourceSelectValue(),
          securityGroups: [],
        }),
      },
    });
    expect(errors).toEqual({
      spec: {
        networkAttachment: {
          value: {
            virtualNetwork: { id: 'Virtual network is required' },
            subnet: { id: 'Subnet is required' },
            securityGroups: 'Select at least one security group',
          },
        },
      },
    });
  });

  it('accepts valid custom networking when useDefaultNetwork is false', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: 'vn-1', name: 'vn-1' },
          subnet: { id: 'subnet-1', name: 'subnet-1' },
          securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
        }),
      },
    });
    expect(errors).toEqual({});
  });

  it('validates networking step with autoExternalIpAttachment set to true', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: clusterCatalogItem.title, id: clusterCatalogItem.id },
      spec: {
        ...emptyValues.spec,
        autoExternalIpAttachment: provisioningFieldValue(true),
      },
    });
    expect(errors).toEqual({});
  });

  it('returns undefined for review step', () => {
    expect(buildClusterStepSchema('review', t)).toBeUndefined();
  });
});
