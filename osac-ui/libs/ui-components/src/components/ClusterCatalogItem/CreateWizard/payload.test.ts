import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import {
  buildClusterCatalogItemCreatePayload,
  buildClusterCatalogItemUpdatePayload,
  buildClusterCatalogItemUpdateRequest,
} from './payload';
import { emptyClusterCatalogItemValues } from './values';

describe('buildClusterCatalogItemCreatePayload', () => {
  it('omits editable fields with empty values and creates an unpublished item', () => {
    const values = emptyClusterCatalogItemValues();
    values.metadata.name = 'my-cluster-item';
    values.metadata.project = 'team-a';
    values.description = 'A cluster offering';
    values.template = { id: 'template-1', name: 'cluster-template' };

    const payload = buildClusterCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({
      name: 'my-cluster-item',
      project: 'team-a',
    });
    expect(payload.description).toBe('A cluster offering');
    expect(payload.template).toEqual({ id: 'template-1', name: 'cluster-template' });
    expect(payload.published).toBe(false);
    expect(payload.fields).toEqual({});
  });

  it('encodes a locked version and an editable network default', () => {
    const values = emptyClusterCatalogItemValues();
    values.version = { value: { id: 'version-1', name: '4-17-0' }, locked: true };
    values.podCidr = { value: '10.128.0.0/14', locked: false };

    const payload = buildClusterCatalogItemCreatePayload(values);

    expect(payload.fields?.version).toEqual({
      behavior: {
        case: 'locked',
        value: { id: 'version-1', name: '4-17-0', shared: true },
      },
    });
    expect(payload.fields?.network).toEqual({
      podCidr: { behavior: { case: 'editable', value: { defaultValue: '10.128.0.0/14' } } },
    });
  });

  it('marks an editable cluster version default as shared', () => {
    const values = emptyClusterCatalogItemValues();
    values.version = { value: { id: 'version-2', name: '4-20-0' }, locked: false };

    const payload = buildClusterCatalogItemCreatePayload(values);

    expect(payload.fields?.version).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { id: 'version-2', name: '4-20-0', shared: true } },
      },
    });
  });

  it('maps node-set rows into the policy map and omits an empty editable map', () => {
    const values = emptyClusterCatalogItemValues();
    values.nodeSets = {
      locked: true,
      value: [
        {
          rowId: 'workers-row',
          name: 'workers',
          hostType: { id: 'bmit-worker', name: 'worker' },
          size: '3',
        },
        {
          rowId: 'infra-row',
          name: 'infra',
          hostType: { id: 'bmit-infra', name: 'infra' },
          size: '1',
        },
        {
          rowId: 'legacy-row',
          name: 'legacy',
          hostType: { id: 'bmit-legacy', name: '' },
          size: '1',
        },
      ],
    };

    expect(buildClusterCatalogItemCreatePayload(values).fields?.nodeSets).toEqual({
      behavior: {
        case: 'locked',
        value: {
          items: {
            workers: {
              size: 3,
              baremetalInstanceType: { id: 'bmit-worker', name: 'worker', shared: true },
            },
            infra: {
              size: 1,
              baremetalInstanceType: { id: 'bmit-infra', name: 'infra', shared: true },
            },
            legacy: {
              size: 1,
              baremetalInstanceType: { id: 'bmit-legacy', shared: true },
            },
          },
        },
      },
    });

    const emptyValues = emptyClusterCatalogItemValues();
    emptyValues.nodeSets = { value: [], locked: false };
    expect(buildClusterCatalogItemCreatePayload(emptyValues).fields?.nodeSets).toBeUndefined();
  });

  it('encodes global and tenant-scoped metadata consistently with the general step', () => {
    const globalValues = emptyClusterCatalogItemValues();
    globalValues.scope = 'global';
    globalValues.metadata.name = 'global-item';
    expect(buildClusterCatalogItemCreatePayload(globalValues).metadata).toEqual({
      name: 'global-item',
      tenant: 'shared',
    });

    const tenantValues = emptyClusterCatalogItemValues();
    tenantValues.metadata.name = 'tenant-item';
    tenantValues.metadata.tenant = { id: 'tenant-1', name: 'Tenant One' };
    expect(buildClusterCatalogItemCreatePayload(tenantValues).metadata).toEqual({
      name: 'tenant-item',
      tenant: 'tenant-1',
    });
  });
});

describe('buildClusterCatalogItemUpdatePayload', () => {
  it('omits immutable general fields and template from updates', () => {
    const values = emptyClusterCatalogItemValues();
    values.metadata.name = 'unchanged-name';
    values.metadata.project = 'team-a';
    values.metadata.tenant = { id: 'tenant-1', name: '' };
    values.template = { id: 'template-1', name: '' };
    values.description = 'Updated description';
    values.podCidr = { value: '10.128.0.0/14', locked: true };

    const payload = buildClusterCatalogItemUpdatePayload(
      create(ClusterCatalogItemSchema, { id: 'item-1' }),
      values,
    );

    expect(payload.metadata).toBeUndefined();
    expect(payload.description).toBe('Updated description');
    expect(payload.template).toBeUndefined();
    expect(payload.fields?.network?.podCidr).toEqual({
      behavior: { case: 'locked', value: '10.128.0.0/14' },
    });
  });

  it('includes clear paths for policies omitted from an edit', () => {
    const item = create(ClusterCatalogItemSchema, {
      id: 'item-1',
      fields: {
        version: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { name: '4-17-0', shared: true } },
          },
        },
        network: {
          serviceCidr: {
            behavior: { case: 'editable', value: { defaultValue: '10.0.0.0/16' } },
          },
        },
      },
    });

    const request = buildClusterCatalogItemUpdateRequest(item, emptyClusterCatalogItemValues());

    expect(request.object.fields).toHaveProperty('version', undefined);
    expect(request.object.fields?.network).toEqual({
      podCidr: undefined,
      serviceCidr: undefined,
    });
  });

  it('clears one network policy while retaining its sibling policy', () => {
    const item = create(ClusterCatalogItemSchema, {
      id: 'item-1',
      fields: {
        network: {
          podCidr: {
            behavior: { case: 'editable', value: { defaultValue: '10.128.0.0/14' } },
          },
          serviceCidr: {
            behavior: { case: 'editable', value: { defaultValue: '10.0.0.0/16' } },
          },
        },
      },
    });
    const values = emptyClusterCatalogItemValues();
    values.podCidr = { value: '10.129.0.0/14', locked: false };

    const request = buildClusterCatalogItemUpdateRequest(item, values);

    expect(request.object.fields?.network).toEqual({
      podCidr: {
        behavior: { case: 'editable', value: { defaultValue: '10.129.0.0/14' } },
      },
      serviceCidr: undefined,
    });
  });
});
