import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import { ComputeInstanceTemplateReferenceSchema } from '@osac/types';
import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import type { ComputeInstanceWizardValues } from './fields';
import { buildComputeInstanceStepSchema } from './schemas';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-rhel-9',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  template: create(ComputeInstanceTemplateReferenceSchema, {
    id: 'tpl-rhel-9',
    name: 'tpl-rhel-9',
  }),
  published: true,
  templateParameters: {},
};

const emptyValues: ComputeInstanceWizardValues = {
  catalogItemReference: { name: '', id: '' },
  metadata: { name: '', project: '' },
  spec: {
    sshKey: provisioningFieldValue(''),
    diskImage: provisioningFieldValue(emptyResourceSelectValue()),
    instanceType: provisioningFieldValue(''),
    userDataSource: provisioningFieldValue('inline'),
    userData: provisioningFieldValue(''),
    userDataSecret: provisioningFieldValue(''),
    bootDisk: {
      sizeGib: provisioningFieldValue(''),
      storageTier: provisioningFieldValue(emptyResourceSelectValue()),
    },
    additionalDisks: provisioningFieldValue([]),
    networking: {
      networkAttachment: provisioningFieldValue({
        useDefaultNetwork: true,
        virtualNetwork: emptyResourceSelectValue(),
        subnet: emptyResourceSelectValue(),
        securityGroups: [],
      }),
      autoExternalIpAttachment: provisioningFieldValue(false),
    },
  },
};

const validateStep = async (
  stepId: Parameters<typeof buildComputeInstanceStepSchema>[0],
  values: ComputeInstanceWizardValues,
) => {
  const schema = buildComputeInstanceStepSchema(stepId, tIdentity);
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

describe('buildComputeInstanceStepSchema', () => {
  it('requires catalog item on catalog step', async () => {
    const errors = await validateStep('catalog', emptyValues);
    expect(errors).toEqual({
      catalogItemReference: {
        name: 'catalogProvision.validation.catalogItemRequired',
        id: 'catalogProvision.validation.catalogItemRequired',
      },
    });
  });

  it('requires name on general step without validating configuration fields', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: '', project: '' },
      spec: {
        ...emptyValues.spec,
        diskImage: provisioningFieldValue({ id: 'disk-image-rhel', name: 'rhel' }),
      },
    });
    expect(errors).toEqual({ metadata: { name: 'Name is required' } });
  });

  it('rejects invalid DNS label names on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'MyVM', project: '' },
      spec: {
        ...emptyValues.spec,
        diskImage: provisioningFieldValue({ id: 'disk-image-rhel', name: 'rhel' }),
      },
    });
    expect(errors).toEqual({
      metadata: {
        name: 'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
      },
    });
  });

  it('accepts valid DNS label name on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'my-vm', project: '' },
      spec: {
        ...emptyValues.spec,
        diskImage: provisioningFieldValue({ id: 'disk-image-rhel', name: 'rhel' }),
      },
    });
    expect(errors).toEqual({});
  });

  it('requires a disk image on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'my-vm', project: '' },
    });
    expect(errors).toEqual({
      spec: { diskImage: { value: { id: 'Disk image is required' } } },
    });
  });

  it('validates boot disk as numeric on storage step', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('not-a-number'),
          storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
        },
      },
    });
    expect(errors).toEqual({
      spec: { bootDisk: { sizeGib: { value: 'catalogProvision.validation.bootDiskNumber' } } },
    });
  });

  it('does not validate boot disk on configuration step', async () => {
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        instanceType: provisioningFieldValue('standard-4-8'),
        bootDisk: {
          sizeGib: provisioningFieldValue('not-a-number'),
          storageTier: provisioningFieldValue(emptyResourceSelectValue()),
        },
      },
    });
    expect(errors).toEqual({});
  });

  it('does not require networking when leaving the storage step', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('30'),
          storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
        },
      },
    });
    expect(errors).toEqual({});
  });

  it('does not require networking pickers on networking step', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
      },
    });
    expect(errors).toEqual({});
  });

  it('requires instance type on configuration step', async () => {
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
      },
    });
    expect(errors).toEqual({
      spec: {
        instanceType: { value: 'Instance type is required' },
      },
    });
  });

  it('validates inline user data with the shared 64 KB limit', async () => {
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        instanceType: provisioningFieldValue('standard-4-8'),
        userData: provisioningFieldValue('a'.repeat(65537)),
      },
    });
    expect(errors).toEqual({
      spec: { userData: { value: 'User data must not exceed 64 KB.' } },
    });

    await expect(
      validateStep('configuration', {
        ...emptyValues,
        catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          instanceType: provisioningFieldValue('standard-4-8'),
          userData: provisioningFieldValue('a'.repeat(65536)),
        },
      }),
    ).resolves.toEqual({});

    await expect(
      validateStep('configuration', {
        ...emptyValues,
        catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          instanceType: provisioningFieldValue('standard-4-8'),
          userDataSource: provisioningFieldValue('secret'),
          userData: provisioningFieldValue('a'.repeat(65537)),
          userDataSecret: provisioningFieldValue('user-data-secret'),
        },
      }),
    ).resolves.toEqual({});
  });

  it('requires boot disk on storage step', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
    });
    expect(errors).toEqual({
      spec: {
        bootDisk: {
          sizeGib: { value: 'catalogProvision.validation.required' },
          storageTier: { value: 'Storage tier is required' },
        },
      },
    });
  });

  it('requires storage tier on each additional disk on storage step', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('30'),
          storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
        },
        additionalDisks: provisioningFieldValue([
          { sizeGib: '100', storageTier: emptyResourceSelectValue() },
        ]),
      },
    });
    expect(errors).toEqual({
      spec: {
        additionalDisks: {
          'value[0]': {
            storageTier: 'Storage tier is required',
          },
        },
      },
    });
  });

  it('requires a boot disk storage tier on storage step', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('30'),
          storageTier: provisioningFieldValue(emptyResourceSelectValue()),
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        bootDisk: { storageTier: { value: 'Storage tier is required' } },
      },
    });
  });

  it('accepts a name-only storage tier on additional disks', async () => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('30'),
          storageTier: provisioningFieldValue({ id: '', name: 'fast' }),
        },
        additionalDisks: provisioningFieldValue([
          { sizeGib: '100', storageTier: { id: '', name: 'fast' } },
        ]),
      },
    });
    expect(errors).toEqual({});
  });

  it.each([
    ['blank', ''],
    ['non-numeric', 'not-a-number'],
    ['zero', '0'],
    ['negative', '-5'],
    ['fractional', '30.5'],
    ['above the maximum', '16385'],
  ])('rejects an additional disk size that is %s', async (_label, sizeGib) => {
    const errors = await validateStep('storage', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        bootDisk: {
          sizeGib: provisioningFieldValue('30'),
          storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
        },
        additionalDisks: provisioningFieldValue([
          { sizeGib, storageTier: { id: 'id-fast', name: 'fast' } },
        ]),
      },
    });
    expect(errors).toEqual({
      spec: {
        additionalDisks: {
          'value[0]': {
            sizeGib: 'Additional disk size must be a number',
          },
        },
      },
    });
  });

  it.each([
    ['the minimum', '1'],
    ['the maximum', '16384'],
    ['a typical value', '100'],
  ])(
    'accepts an additional disk size at %s with a storage tier selected',
    async (_label, sizeGib) => {
      const errors = await validateStep('storage', {
        ...emptyValues,
        catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: {
            sizeGib: provisioningFieldValue('30'),
            storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
          },
          additionalDisks: provisioningFieldValue([
            { sizeGib, storageTier: { id: 'id-fast', name: 'fast' } },
          ]),
        },
      });
      expect(errors).toEqual({});
    },
  );

  it('does not validate additional disks on configuration step', async () => {
    const errors = await validateStep('configuration', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        instanceType: provisioningFieldValue('standard-4-8'),
        additionalDisks: provisioningFieldValue([
          { sizeGib: '100', storageTier: emptyResourceSelectValue() },
        ]),
      },
    });
    expect(errors).toEqual({});
  });

  it('does not require networking pickers when useDefaultNetwork is true', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        networking: {
          networkAttachment: provisioningFieldValue({
            useDefaultNetwork: true,
            virtualNetwork: emptyResourceSelectValue(),
            subnet: emptyResourceSelectValue(),
            securityGroups: [],
          }),
          autoExternalIpAttachment: provisioningFieldValue(false),
        },
      },
    });
    expect(errors).toEqual({});
  });

  it('requires virtual network and subnet when useDefaultNetwork is false', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        networking: {
          networkAttachment: provisioningFieldValue({
            useDefaultNetwork: false,
            virtualNetwork: emptyResourceSelectValue(),
            subnet: emptyResourceSelectValue(),
            securityGroups: [],
          }),
          autoExternalIpAttachment: provisioningFieldValue(false),
        },
      },
    });
    expect(errors).toEqual({
      spec: {
        networking: {
          networkAttachment: {
            value: {
              virtualNetwork: { id: 'Virtual network is required' },
              subnet: { id: 'Subnet is required' },
            },
          },
        },
      },
    });
  });

  it('accepts valid custom networking when useDefaultNetwork is false', async () => {
    const errors = await validateStep('networking', {
      ...emptyValues,
      catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
      metadata: { name: 'web-01', project: '' },
      spec: {
        ...emptyValues.spec,
        networking: {
          networkAttachment: provisioningFieldValue({
            useDefaultNetwork: false,
            virtualNetwork: { id: 'vn-1', name: 'vn-1' },
            subnet: { id: 'subnet-1', name: 'subnet-1' },
            securityGroups: [],
          }),
          autoExternalIpAttachment: provisioningFieldValue(false),
        },
      },
    });
    expect(errors).toEqual({});
  });

  it('returns undefined for review step', () => {
    expect(buildComputeInstanceStepSchema('review', tIdentity)).toBeUndefined();
  });
});
