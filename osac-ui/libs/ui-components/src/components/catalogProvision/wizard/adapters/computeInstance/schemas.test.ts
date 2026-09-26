import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import { ComputeInstanceTemplateReferenceSchema } from '@osac/types';
import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import type { ComputeInstanceWizardValues } from './fields';
import { buildComputeInstanceStepSchema } from './schemas';
import { emptyResourceSelectValue } from '../../../../Form/resourceSelectValue';

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
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshKey: { name: '' },
    instanceType: '',
    userData: '',
    bootDisk: { sizeGib: '', storageTier: emptyResourceSelectValue() },
    additionalDisks: [],
    networking: {
      virtualNetwork: '',
      subnet: '',
      securityGroups: [],
    },
  },
};

const validateStep = async (
  stepId: Parameters<typeof buildComputeInstanceStepSchema>[1],
  values: ComputeInstanceWizardValues,
  catalogItem: unknown = null,
) => {
  const schema = buildComputeInstanceStepSchema(catalogItem, stepId, tIdentity);
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
    expect(errors).toEqual({ catalogItemId: 'catalogProvision.validation.catalogItemRequired' });
  });

  it('requires name on general step without validating configuration fields', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: vmCatalogItem.id,
      metadata: { name: '', project: '' },
    });
    expect(errors).toEqual({ metadata: { name: 'Name is required' } });
  });

  it('rejects invalid DNS label names on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: vmCatalogItem.id,
      metadata: { name: 'MyVM', project: '' },
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
      catalogItemId: vmCatalogItem.id,
      metadata: { name: 'my-vm', project: '' },
    });
    expect(errors).toEqual({});
  });

  it('does not require an SSH key on general step', async () => {
    const errors = await validateStep('general', {
      ...emptyValues,
      catalogItemId: vmCatalogItem.id,
      metadata: { name: 'my-vm', project: '' },
    });
    expect(errors).toEqual({});
  });

  it('validates boot disk as numeric on storage step', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: 'not-a-number', storageTier: { id: 'id-fast', name: 'fast' } },
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: { bootDisk: { sizeGib: 'catalogProvision.validation.bootDiskNumber' } },
    });
  });

  it('does not validate boot disk on configuration step', async () => {
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          instanceType: 'standard-4-8',
          bootDisk: { sizeGib: 'not-a-number', storageTier: emptyResourceSelectValue() },
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({});
  });

  it('does not require networking when leaving the storage step', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: '30', storageTier: { id: 'id-fast', name: 'fast' } },
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({});
  });

  it('requires networking pickers on networking step', async () => {
    const errors = await validateStep(
      'networking',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        networking: {
          virtualNetwork: 'catalogProvision.validation.virtualNetworkRequired',
          subnet: 'catalogProvision.validation.subnetRequired',
          securityGroups: 'catalogProvision.validation.securityGroupRequired',
        },
      },
    });
  });

  it('requires instance type on configuration step', async () => {
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        instanceType: 'catalogProvision.validation.instanceTypeRequired',
      },
    });
  });

  it('requires boot disk on storage step', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        bootDisk: {
          sizeGib: 'catalogProvision.validation.required',
          storageTier: 'Storage tier is required',
        },
      },
    });
  });

  it('requires storage tier on each additional disk on storage step', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: '30', storageTier: { id: 'id-fast', name: 'fast' } },
          additionalDisks: [{ sizeGib: '100', storageTier: emptyResourceSelectValue() }],
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        'additionalDisks[0]': {
          storageTier: 'Storage tier is required',
        },
      },
    });
  });

  it('requires a boot disk storage tier on storage step', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: '30', storageTier: emptyResourceSelectValue() },
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        bootDisk: { storageTier: 'Storage tier is required' },
      },
    });
  });

  it('accepts a name-only storage tier on additional disks', async () => {
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: '30', storageTier: { id: '', name: 'fast' } },
          additionalDisks: [{ sizeGib: '100', storageTier: { id: '', name: 'fast' } }],
        },
      },
      vmCatalogItem,
    );
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
    const errors = await validateStep(
      'storage',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          bootDisk: { sizeGib: '30', storageTier: { id: 'id-fast', name: 'fast' } },
          additionalDisks: [{ sizeGib, storageTier: { id: 'id-fast', name: 'fast' } }],
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({
      spec: {
        'additionalDisks[0]': {
          sizeGib: 'Additional disk size must be a number',
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
      const errors = await validateStep(
        'storage',
        {
          ...emptyValues,
          catalogItemId: vmCatalogItem.id,
          metadata: { name: 'web-01', project: '' },
          spec: {
            ...emptyValues.spec,
            bootDisk: { sizeGib: '30', storageTier: { id: 'id-fast', name: 'fast' } },
            additionalDisks: [{ sizeGib, storageTier: { id: 'id-fast', name: 'fast' } }],
          },
        },
        vmCatalogItem,
      );
      expect(errors).toEqual({});
    },
  );

  it('does not validate additional disks on configuration step', async () => {
    const errors = await validateStep(
      'configuration',
      {
        ...emptyValues,
        catalogItemId: vmCatalogItem.id,
        metadata: { name: 'web-01', project: '' },
        spec: {
          ...emptyValues.spec,
          instanceType: 'standard-4-8',
          additionalDisks: [{ sizeGib: '100', storageTier: emptyResourceSelectValue() }],
        },
      },
      vmCatalogItem,
    );
    expect(errors).toEqual({});
  });

  it('returns undefined for review step', () => {
    expect(buildComputeInstanceStepSchema(vmCatalogItem, 'review', tIdentity)).toBeUndefined();
  });
});
