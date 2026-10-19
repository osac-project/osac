import { describe, expect, it } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdatePayload,
} from './payload';
import { emptyComputeCatalogItemValues } from './values';
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
    expect(payload.metadata).toEqual({ name: 'my-item', description: '', project: 'team-a' });
    expect(payload.template).toEqual({ id: 'template-1' });
  });

  it('encodes an admin global-scope item with the shared tenant sentinel', () => {
    const values = emptyComputeCatalogItemValues();
    values.scope = 'global';
    values.metadata.name = 'global-item';

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({ name: 'global-item', description: '', tenant: 'shared' });
  });

  it('encodes an admin tenant-scoped item with the selected tenant id', () => {
    const values = emptyComputeCatalogItemValues();
    values.scope = 'tenant';
    values.metadata.name = 'tenant-item';
    values.metadata.tenant = { id: 'tenant-1', name: 'Tenant One' };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({ name: 'tenant-item', description: '', tenant: 'tenant-1' });
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

  it('omits userData when editable and empty, and sends it as a default when editable and set', () => {
    const emptyValues = emptyComputeCatalogItemValues();
    expect(buildComputeCatalogItemCreatePayload(emptyValues).fields?.userData).toBeUndefined();

    const filledValues = emptyComputeCatalogItemValues();
    filledValues.userData = { value: 'echo hi', locked: false };
    expect(buildComputeCatalogItemCreatePayload(filledValues).fields?.userData).toEqual({
      behavior: { case: 'editable', value: { defaultValue: 'echo hi' } },
    });
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
    values.metadata.description = 'updated description';
    values.metadata.tenant = { id: 'tenant-1', name: '' };
    values.metadata.project = 'team-a';
    values.template = { id: 'template-1', name: '' };
    values.userData = { value: 'echo hi', locked: true };

    const payload = buildComputeCatalogItemUpdatePayload(item, values);

    expect(payload.metadata).toEqual({ description: 'updated description' });
    expect(payload.template).toBeUndefined();
    expect(payload.fields?.userData).toEqual({ behavior: { case: 'locked', value: 'echo hi' } });
  });
});
