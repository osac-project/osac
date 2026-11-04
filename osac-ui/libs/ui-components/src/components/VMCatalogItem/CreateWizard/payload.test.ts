import { describe, expect, it } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdatePayload,
  buildComputeCatalogItemUpdateRequest,
} from './payload';
import { emptyComputeCatalogItemValues, getComputeCatalogItemInitialValues } from './values';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

describe('buildComputeCatalogItemCreatePayload', () => {
  it('omits every governed field when all are editable and empty', () => {
    const values = emptyComputeCatalogItemValues();
    values.metadata.name = 'my-item';
    values.metadata.project = 'team-a';
    values.description = 'A virtual machine offering';
    values.template = { id: 'template-1', name: 'compute-template' };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields).toEqual({});
    expect(payload.description).toBe('A virtual machine offering');
    expect(payload.published).toBe(false);
    expect(payload.metadata).toEqual({ name: 'my-item', project: 'team-a' });
    expect(payload.template).toEqual({ id: 'template-1', name: 'compute-template' });
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
      behavior: { case: 'locked', value: { id: 'img-1', name: 'rhel-9' } },
    });
  });

  it('writes an editable resource field as a default value', () => {
    const values = emptyComputeCatalogItemValues();
    values.instanceType = { value: { id: 'it-1', name: 'standard-4-8' }, locked: false };

    const payload = buildComputeCatalogItemCreatePayload(values);

    expect(payload.fields?.instanceType).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { id: 'it-1', name: 'standard-4-8' } },
      },
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

  it('deserializes inline user data and preserves Secret user-data sources', () => {
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
    expect(secretValues.userDataSecret).toEqual({ value: 'cloud-init', locked: false });

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
    expect(lockedSecretValues.userDataSecret).toEqual({ value: 'cloud-init', locked: true });
  });

  it('preserves a Secret source when editing without inline user data', () => {
    const item = {
      fields: {
        userDataSource: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { source: { case: 'secret', value: { name: 'cloud-init' } } } },
          },
        },
      },
    } as ComputeInstanceCatalogItem;

    const values = getComputeCatalogItemInitialValues(item);
    const payload = buildComputeCatalogItemUpdatePayload(item, values);

    expect(payload.fields?.userDataSource).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'secret', value: { name: 'cloud-init' } } } },
      },
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
        value: { items: [{ sizeGib: 20, storageTier: { id: 'tier-1', name: 'fast' } }] },
      },
    });

    const editableEmptyValues = emptyComputeCatalogItemValues();
    expect(
      buildComputeCatalogItemCreatePayload(editableEmptyValues).fields?.additionalDisks,
    ).toBeUndefined();
  });
});

describe('compute catalog item initial values', () => {
  it('leaves the disk image family undefined when loading an edit item', () => {
    const values = getComputeCatalogItemInitialValues({
      fields: {
        diskImage: {
          behavior: { case: 'locked', value: { id: 'image-1', name: 'windows-image' } },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.guestOsFamily).toBeUndefined();
    expect(values.diskImage.value).toEqual({ id: 'image-1', name: 'windows-image' });
  });
});

describe('buildComputeCatalogItemUpdatePayload', () => {
  const item = { id: 'catalog-item-1' } as ComputeInstanceCatalogItem;

  it('updates a resource reference name together with its id', () => {
    const item = {
      id: 'catalog-item-1',
      fields: {
        diskImage: {
          behavior: { case: 'locked', value: { id: 'img-1', name: 'old-image' } },
        },
      },
    } as ComputeInstanceCatalogItem;
    const values = emptyComputeCatalogItemValues();
    values.diskImage = { value: { id: 'img-2', name: 'new-image' }, locked: true };

    const request = buildComputeCatalogItemUpdateRequest(item, values);

    expect(request.object.fields?.diskImage).toEqual({
      behavior: { case: 'locked', value: { id: 'img-2', name: 'new-image' } },
    });
    expect(request.object.fields?.diskImage).toEqual({
      behavior: { case: 'locked', value: { id: 'img-2', name: 'new-image' } },
    });
  });

  it('omits name, tenant, project, and template, and includes the governed fields', () => {
    const values = emptyComputeCatalogItemValues();
    values.metadata.name = 'should-not-be-sent';
    values.metadata.tenant = { id: 'tenant-1', name: '' };
    values.metadata.project = 'team-a';
    values.template = { id: 'template-1', name: '' };
    values.description = 'Updated description';
    values.userData = { value: 'echo hi', locked: true };

    const payload = buildComputeCatalogItemUpdatePayload(item, values);

    expect(payload.metadata).toBeUndefined();
    expect(payload.description).toBe('Updated description');
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

    expect(request.object.fields).toHaveProperty('instanceType', undefined);
    expect(request.object.fields).toHaveProperty('userDataSource', undefined);
  });

  it('clears one boot-disk policy while retaining its sibling policy', () => {
    const item = {
      id: 'catalog-item-1',
      fields: {
        bootDisk: {
          sizeGib: { behavior: { case: 'locked', value: 40 } },
          storageTier: {
            behavior: {
              case: 'editable',
              value: { defaultValue: { id: 'tier-1', name: 'fast' } },
            },
          },
        },
      },
    } as ComputeInstanceCatalogItem;
    const values = emptyComputeCatalogItemValues();
    values.bootDiskStorageTier = {
      value: { id: 'tier-2', name: 'standard' },
      locked: false,
    };

    const request = buildComputeCatalogItemUpdateRequest(item, values);

    expect(request.object.fields?.bootDisk).toEqual({
      sizeGib: undefined,
      storageTier: {
        behavior: {
          case: 'editable',
          value: { defaultValue: { id: 'tier-2', name: 'standard' } },
        },
      },
    });
  });
});
