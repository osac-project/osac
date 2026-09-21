import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import type { ClusterCatalogItem } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import { createEmptyNodeSetRow } from './fields';
import { buildClusterStepSchema } from './schemas';
import { tIdentity as t } from '../../../../../test-utils/i18n';

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
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshPublicKey: '',
    pullSecretSecret: {
      name: '',
    },
    versionName: '',
    nodeSetRows: [],
    network: {
      podCidr: '',
      serviceCidr: '',
    },
  },
};

const validateStep = async (
  stepId: Parameters<typeof buildClusterStepSchema>[1],
  values: ClusterWizardValues,
  catalogItem: ClusterCatalogItem | null = null,
) => {
  const schema = buildClusterStepSchema(catalogItem, stepId, t);
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
    expect(errors).toEqual({ catalogItemId: 'Select a catalog item' });
  });

  it('rejects invalid DNS label names on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'MyCluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: {
          name: 'foo',
        },
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
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: {
          name: '',
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        pullSecretSecret: {
          name: 'Pull secret is required',
        },
      },
    });
  });

  it('rejects malformed ssh public key on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'my-cluster', project: '' },
      spec: {
        ...emptyValues.spec,
        pullSecretSecret: {
          name: 'foo',
        },
        sshPublicKey: 'not-a-key',
      },
    });
    expect(errors).toEqual({
      spec: {
        sshPublicKey:
          'SSH public key must be in the form "[TYPE] key [[EMAIL]]". Supported types are ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
      },
    });
  });

  it('requires pull secret on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: clusterCatalogItem.id,
      metadata: { name: 'my-cluster', project: '' },
    });
    expect(errors).toEqual({
      spec: { pullSecretSecret: { name: 'Pull secret is required' } },
    });
  });

  it('requires a version on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: {
            name: 'foo',
          },
          versionName: '',
          nodeSetRows: [{ ...row, name: 'workers', hostType: 'acme_1tb', size: '3' }],
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: { versionName: 'Version is required' },
    });
  });

  it('requires at least one node set on configuration step', async () => {
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: {
            name: 'foo',
          },
          versionName: '4-17-0',
          nodeSetRows: [],
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: { nodeSetRows: 'At least one node set is required' },
    });
  });

  it('requires positive pool sizes on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: {
            name: 'foo',
          },
          versionName: '4-17-0',
          nodeSetRows: [
            {
              ...row,
              name: 'workers',
              hostType: 'acme_1tb',
              size: '0',
            },
          ],
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        'nodeSetRows[0]': { size: 'Pool size must be greater than zero' },
      },
    });
  });

  it('allows duplicate host types when node set IDs are distinct', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: {
            name: 'foo',
          },
          versionName: '4-17-0',
          nodeSetRows: [
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
          ],
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({});
  });

  it('rejects duplicate node set IDs on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: { name: 'foo' },
          versionName: '4-17-0',
          nodeSetRows: [
            { ...row, rowId: 'row-1', name: 'production', hostType: 'acme_1tb', size: '3' },
            { ...row, rowId: 'row-2', name: 'production', hostType: 'acme_1tb', size: '2' },
          ],
        },
      },
      clusterCatalogItem,
    );

    expect(errors).toEqual({
      spec: {
        'nodeSetRows[1]': { name: 'Node set names must be unique' },
      },
    });
  });

  it('rejects empty node set IDs on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: { name: 'foo' },
          versionName: '4-17-0',
          nodeSetRows: [{ ...row, name: '', hostType: 'acme_1tb', size: '3' }],
        },
      },
      clusterCatalogItem,
    );

    expect(errors).toEqual({
      spec: {
        'nodeSetRows[0]': { name: 'Name is required' },
      },
    });
  });

  it('rejects invalid node set names on configuration step', async () => {
    const row = createEmptyNodeSetRow();
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        metadata: { name: 'my-cluster', project: '' },
        spec: {
          ...emptyValues.spec,
          pullSecretSecret: { name: 'foo' },
          versionName: '4-17-0',
          nodeSetRows: [{ ...row, name: 'Workers_1', hostType: 'acme_1tb', size: '3' }],
        },
      },
      clusterCatalogItem,
    );

    expect(errors).toEqual({
      spec: {
        'nodeSetRows[0]': {
          name: 'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
        },
      },
    });
  });

  it('validates CIDR format on networking step when values are present', async () => {
    const errors = await validateStep(
      'networking',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        spec: {
          ...emptyValues.spec,
          network: {
            podCidr: 'invalid',
            serviceCidr: '',
          },
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        network: {
          podCidr: 'Invalid IPv4 CIDR notation',
        },
      },
    });
  });

  it('rejects IPv6 pod and service CIDRs on networking step', async () => {
    const errors = await validateStep(
      'networking',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        spec: {
          ...emptyValues.spec,
          network: {
            podCidr: 'fd01::/48',
            serviceCidr: 'fd02::/112',
          },
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        network: {
          podCidr: 'Invalid IPv4 CIDR notation',
          serviceCidr: 'Invalid IPv4 CIDR notation',
        },
      },
    });
  });

  it('rejects overlapping pod and service CIDRs on networking step', async () => {
    const errors = await validateStep(
      'networking',
      {
        ...emptyValues,
        catalogItemId: clusterCatalogItem.id,
        spec: {
          ...emptyValues.spec,
          network: {
            podCidr: '10.128.0.0/14',
            serviceCidr: '10.128.0.0/14',
          },
        },
      },
      clusterCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        network: {
          serviceCidr: 'Service CIDR must not overlap the pod CIDR.',
        },
      },
    });
  });

  it('returns undefined for review step', () => {
    expect(buildClusterStepSchema(clusterCatalogItem, 'review', t)).toBeUndefined();
  });
});
