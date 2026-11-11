import { describe, expect, it } from 'vitest';

import type { BareMetalInstanceCatalogItem } from '@osac/types';

import {
  buildBareMetalCatalogItemCreatePayload,
  buildBareMetalCatalogItemUpdatePayload,
  buildBareMetalCatalogItemUpdateRequest,
} from './payload';
import { emptyBareMetalCatalogItemValues, getBareMetalCatalogItemInitialValues } from './values';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

describe('buildBareMetalCatalogItemCreatePayload', () => {
  it('omits editable fields with empty values and creates an unpublished item', () => {
    const values = emptyBareMetalCatalogItemValues();
    values.metadata.name = 'my-bare-metal-item';
    values.metadata.project = 'team-a';
    values.description = 'A bare metal offering';
    values.template = { id: 'template-1', name: 'baremetal-template' };

    const payload = buildBareMetalCatalogItemCreatePayload(values);

    expect(payload.metadata).toEqual({
      name: 'my-bare-metal-item',
      project: 'team-a',
    });
    expect(payload.description).toBe('A bare metal offering');
    expect(payload.template).toEqual({ id: 'template-1', name: 'baremetal-template' });
    expect(payload.published).toBe(false);
    expect(payload.fields).toEqual({});
  });

  it('encodes locked instance type, disk image, and user data values', () => {
    const values = emptyBareMetalCatalogItemValues();
    values.instanceType = {
      value: { id: 'instance-type-1', name: 'acme-1tb' },
      locked: true,
    };
    values.diskImage = { value: { id: 'image-1', name: 'rhel-9' }, locked: true };
    values.userData = { value: '  #cloud-config\nusers: []  ', locked: true };

    const payload = buildBareMetalCatalogItemCreatePayload(values);

    expect(payload.fields?.instanceType).toEqual({
      behavior: {
        case: 'locked',
        value: { id: 'instance-type-1', name: 'acme-1tb', tenant: 'shared' },
      },
    });
    expect(payload.fields?.diskImage).toEqual({
      behavior: { case: 'locked', value: { id: 'image-1', name: 'rhel-9' } },
    });
    expect(payload.fields?.userDataSource).toEqual({
      behavior: {
        case: 'locked',
        value: { source: { case: 'inline', value: '#cloud-config\nusers: []' } },
      },
    });
  });

  it('encodes editable defaults and omits an empty resource selector', () => {
    const values = emptyBareMetalCatalogItemValues();
    values.instanceType = {
      value: { id: 'instance-type-1', name: 'acme-1tb' },
      locked: false,
    };
    values.diskImage = { value: emptyResourceSelectValue(), locked: false };
    values.userData = { value: 'echo hi', locked: false };

    const payload = buildBareMetalCatalogItemCreatePayload(values);

    expect(payload.fields?.instanceType).toEqual({
      behavior: {
        case: 'editable',
        value: {
          defaultValue: { id: 'instance-type-1', name: 'acme-1tb', tenant: 'shared' },
        },
      },
    });
    expect(payload.fields?.diskImage).toBeUndefined();
    expect(payload.fields?.userDataSource).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'inline', value: 'echo hi' } } },
      },
    });
  });

  it('encodes global and tenant-scoped metadata consistently with the general step', () => {
    const globalValues = emptyBareMetalCatalogItemValues();
    globalValues.scope = 'global';
    globalValues.metadata.name = 'global-item';
    expect(buildBareMetalCatalogItemCreatePayload(globalValues).metadata).toEqual({
      name: 'global-item',
      tenant: 'shared',
    });

    const tenantValues = emptyBareMetalCatalogItemValues();
    tenantValues.metadata.name = 'tenant-item';
    tenantValues.metadata.tenant = { id: 'tenant-1', name: 'Tenant One' };
    expect(buildBareMetalCatalogItemCreatePayload(tenantValues).metadata).toEqual({
      name: 'tenant-item',
      tenant: 'tenant-1',
    });
  });
});

describe('bare metal catalog item initial values and updates', () => {
  it('leaves the disk image family undefined when loading an edit item', () => {
    const values = getBareMetalCatalogItemInitialValues({
      fields: {
        diskImage: {
          behavior: { case: 'locked', value: { id: 'image-1', name: 'windows-image' } },
        },
      },
    } as BareMetalInstanceCatalogItem);

    expect(values.guestOsFamily).toBeUndefined();
    expect(values.diskImage.value).toEqual({ id: 'image-1', name: 'windows-image' });
  });

  it('loads locked and editable policy values into the shared form shape', () => {
    const item = {
      id: 'catalog-item-1',
      metadata: { name: 'existing-item', tenant: 'tenant-1' },
      template: { id: 'template-1' },
      fields: {
        instanceType: {
          behavior: {
            case: 'locked',
            value: { id: 'instance-type-1', name: 'acme-1tb', shared: true },
          },
        },
        diskImage: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { id: 'image-1', name: 'rhel-9' } },
          },
        },
        userDataSource: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { source: { case: 'inline', value: '' } } },
          },
        },
      },
    } as BareMetalInstanceCatalogItem;

    const values = getBareMetalCatalogItemInitialValues(item);

    expect(values.instanceType).toEqual({
      value: { id: 'instance-type-1', name: 'acme-1tb' },
      locked: true,
    });
    expect(values.diskImage).toEqual({ value: { id: 'image-1', name: 'rhel-9' }, locked: false });
    expect(values.userData).toEqual({ value: '', locked: false });
  });

  it('preserves a Secret user-data source when loading edit values', () => {
    const values = getBareMetalCatalogItemInitialValues({
      fields: {
        userDataSource: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { source: { case: 'secret', value: { name: 'cloud-init' } } } },
          },
        },
      },
    } as BareMetalInstanceCatalogItem);

    expect(values.userData).toEqual({ value: '', locked: false });
    expect(values.userDataSecret).toEqual({ value: 'cloud-init', locked: false });
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
    } as BareMetalInstanceCatalogItem;

    const values = getBareMetalCatalogItemInitialValues(item);
    const payload = buildBareMetalCatalogItemUpdatePayload(item, values);

    expect(payload.fields?.userDataSource).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'secret', value: { name: 'cloud-init' } } } },
      },
    });
  });

  it('omits immutable general fields and the template from updates', () => {
    const values = emptyBareMetalCatalogItemValues();
    values.metadata.name = 'should-not-be-sent';
    values.metadata.tenant = { id: 'tenant-1', name: '' };
    values.metadata.project = 'team-a';
    values.template = { id: 'template-1', name: '' };
    values.description = 'Updated description';
    values.userData = { value: 'echo hi', locked: true };

    const payload = buildBareMetalCatalogItemUpdatePayload(
      { id: 'catalog-item-1' } as BareMetalInstanceCatalogItem,
      values,
    );

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
        instanceType: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { name: 'bare-metal.large', shared: true } },
          },
        },
        diskImage: {
          behavior: { case: 'editable', value: { defaultValue: { id: 'image-1' } } },
        },
      },
    } as BareMetalInstanceCatalogItem;

    const request = buildBareMetalCatalogItemUpdateRequest(item, emptyBareMetalCatalogItemValues());

    expect(request.object.fields).toHaveProperty('instanceType', undefined);
    expect(request.object.fields).toHaveProperty('diskImage', undefined);
  });
});
