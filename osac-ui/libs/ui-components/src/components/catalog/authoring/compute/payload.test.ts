import { describe, expect, it } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdatePayload,
  buildComputeCatalogItemUpdateRequest,
} from './payload';
import { emptyComputeCatalogItemValues, getComputeCatalogItemInitialValues } from './values';
import { emptyResourceSelectValue } from '../../../Form/resourceSelectValue';

describe('buildComputeCatalogItemCreatePayload', () => {
  it('omits every governed field when all are editable and empty', () => {
    const values = emptyComputeCatalogItemValues();
    values.metadata.name = 'my-item';
    values.metadata.project = 'team-a';
    values.template = { id: 'template-1', name: '' };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields).toEqual({});
    expect(payload.published).toBe(false);
    expect(payload.metadata).toEqual({ name: 'my-item', project: 'team-a' });
    expect(payload.template).toEqual({ id: 'template-1' });
  });

  it('encodes an admin global-scope item with the shared tenant sentinel', () => {
    const values = emptyComputeCatalogItemValues();
    values.scope = 'global';
    values.metadata.name = 'global-item';

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({ name: 'global-item', tenant: 'shared' });
  });

  it('encodes an admin tenant-scoped item with the selected tenant id', () => {
    const values = emptyComputeCatalogItemValues();
    values.scope = 'tenant';
    values.metadata.name = 'tenant-item';
    values.metadata.tenant = { id: 'tenant-1', name: 'Tenant One' };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({ name: 'tenant-item', tenant: 'tenant-1' });
  });

  it('writes a locked resource field even if a value is present', () => {
    const values = emptyComputeCatalogItemValues();
    values.diskImage = { value: { id: 'img-1', name: 'rhel-9' }, locked: true };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields?.diskImage).toEqual({
      behavior: { case: 'locked', value: { id: 'img-1' } },
    });
  });

  it('writes an editable resource field as a default value', () => {
    const values = emptyComputeCatalogItemValues();
    values.instanceType = { value: { id: 'it-1', name: 'standard-4-8' }, locked: false };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields?.instanceType).toEqual({
      behavior: { case: 'editable', value: { defaultValue: { id: 'it-1' } } },
    });
  });

  it('omits an editable resource field that is empty', () => {
    const values = emptyComputeCatalogItemValues();
    values.instanceType = { value: emptyResourceSelectValue(), locked: false };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields?.instanceType).toBeUndefined();
  });

  it('encodes a locked boot disk size as a number', () => {
    const values = emptyComputeCatalogItemValues();
    values.bootDiskSizeGib = { value: '40', locked: true };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields?.bootDisk).toEqual({
      sizeGib: { behavior: { case: 'locked', value: 40 } },
    });
  });

  it('omits user data when editable and empty, and sends inline defaults when set', () => {
    const emptyValues = emptyComputeCatalogItemValues();
    expect(
      buildComputeCatalogItemCreatePayload(emptyValues).fields?.userDataSource,
    ).toBeUndefined();

    const filledValues = emptyComputeCatalogItemValues();
    filledValues.userData = { value: 'echo hi', locked: false };
    expect(buildComputeCatalogItemCreatePayload(filledValues).fields?.userDataSource).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'inline', value: 'echo hi' } } },
      },
    });
  });

  it('deserializes inline user data and ignores Secret user-data sources', () => {
    const values = getComputeCatalogItemInitialValues({
      fields: {
        userDataSource: {
          behavior: {
            case: 'locked',
            value: { source: { case: 'inline', value: 'cloud-init' } },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.userData).toEqual({ value: 'cloud-init', locked: true });

    const secretValues = getComputeCatalogItemInitialValues({
      fields: {
        userDataSource: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { source: { case: 'secret', value: { name: 'cloud-init' } } } },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(secretValues.userData).toEqual({ value: '', locked: false });

    const lockedSecretValues = getComputeCatalogItemInitialValues({
      fields: {
        userDataSource: {
          behavior: {
            case: 'locked',
            value: { source: { case: 'secret', value: { name: 'cloud-init' } } },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(lockedSecretValues.userData).toEqual({ value: '', locked: true });
  });

  it('writes a locked additional-disks list and omits an empty editable one', () => {
    const lockedValues = emptyComputeCatalogItemValues();
    lockedValues.additionalDisks = {
      value: [{ sizeGib: '20', storageTier: { id: 'tier-1', name: 'fast' } }],
      locked: true,
    };
    expect(buildComputeCatalogItemCreatePayload(lockedValues).fields?.additionalDisks).toEqual({
      behavior: {
        case: 'locked',
        value: { items: [{ sizeGib: 20, storageTier: { id: 'tier-1' } }] },
      },
    });

    const editableEmptyValues = emptyComputeCatalogItemValues();
    expect(
      buildComputeCatalogItemCreatePayload(editableEmptyValues).fields?.additionalDisks,
    ).toBeUndefined();
  });
});

describe('buildComputeCatalogItemUpdatePayload', () => {
  const item = { id: 'catalog-item-1' } as ComputeInstanceCatalogItem;

  it('omits name, tenant, project, and template, and includes the governed fields', () => {
    const values = emptyComputeCatalogItemValues();
    values.metadata.name = 'should-not-be-sent';
    values.metadata.tenant = { id: 'tenant-1', name: '' };
    values.metadata.project = 'team-a';
    values.template = { id: 'template-1', name: '' };
    values.userData = { value: 'echo hi', locked: true };

    const payload = buildComputeCatalogItemUpdatePayload(item, values);

    expect(payload.metadata).toBeUndefined();
    expect(payload.template).toBeUndefined();
    expect(payload.fields?.userDataSource).toEqual({
      behavior: {
        case: 'locked',
        value: { source: { case: 'inline', value: 'echo hi' } },
      },
    });
  });

  it('includes clear paths for policies omitted from an edit', () => {
    const item = {
      id: 'catalog-item-1',
      fields: {
        instanceType: { behavior: { case: 'editable', value: { defaultValue: { id: 'it-1' } } } },
        userDataSource: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { source: { case: 'inline', value: 'cloud-init' } } },
          },
        },
      },
    } as ComputeInstanceCatalogItem;

    const request = buildComputeCatalogItemUpdateRequest(item, emptyComputeCatalogItemValues());

    expect(request.updateMask.paths).toEqual(
      expect.arrayContaining(['id', 'fields.instance_type', 'fields.user_data_source']),
    );
  });
});
