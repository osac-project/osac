import { describe, expect, it } from 'vitest';

import { type ComputeInstanceCatalogItem, ComputeInstanceRunStrategy } from '@osac/types';

import { buildComputeInstanceCreatePayload, createEmptyComputeInstanceValues } from './payload';
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
  template: {
    $typeName: 'osac.public.v1.ComputeInstanceTemplateReference',
    id: 'tpl-rhel-9',
    name: 'tpl-rhel-9',
    project: '',
    shared: false,
  },
  published: true,
  templateParameters: {},
};

const catalogItemWithAdditionalDisksDefault: ComputeInstanceCatalogItem = {
  ...vmCatalogItem,
  fieldDefinitions: [
    {
      $typeName: 'osac.public.v1.FieldDefinition',
      path: 'spec.additional_disks',
      displayName: 'Additional disks',
      editable: true,
      validationSchema: '',
      default: { kind: { case: 'listValue', value: { values: [] } } },
    },
  ],
} as unknown as ComputeInstanceCatalogItem;

const buildValues = (project: string) => ({
  ...createEmptyComputeInstanceValues(),
  catalogItemId: vmCatalogItem.id,
  metadata: { name: 'my-vm', project },
  spec: {
    ...createEmptyComputeInstanceValues().spec,
    instanceType: 'standard-4-8',
    networking: {
      virtualNetwork: 'vnet-1',
      subnet: 'subnet-1',
      securityGroups: ['sg-1'],
    },
  },
});

const baseValues = () => {
  const values = createEmptyComputeInstanceValues();
  return {
    ...values,
    catalogItemId: vmCatalogItem.id,
    metadata: { name: 'web-01', project: '' },
    spec: {
      ...values.spec,
      instanceType: 'standard-4-8',
      networking: { virtualNetwork: 'vnet', subnet: 'subnet-1', securityGroups: ['sg-1'] },
    },
  };
};

describe('buildComputeInstanceCreatePayload', () => {
  it('builds a catalog-item create payload', () => {
    expect(buildComputeInstanceCreatePayload(buildValues(''), vmCatalogItem)).toEqual({
      metadata: { name: 'my-vm', project: '' },
      spec: {
        catalogItem: { id: vmCatalogItem.id },
        instanceType: { id: 'standard-4-8' },
        runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS,
        networkAttachments: [
          {
            subnet: { id: 'subnet-1' },
            securityGroups: [{ id: 'sg-1' }],
          },
        ],
      },
    });
  });

  it.each([
    ['default (no project)', ''],
    ['top-level project', 'my-project'],
    ['nested project path', 'parent.child'],
  ])('passes the selected %s through to metadata.project', (_label, project) => {
    expect(buildComputeInstanceCreatePayload(buildValues(project), vmCatalogItem).metadata).toEqual(
      {
        name: 'my-vm',
        project,
      },
    );
  });
});

describe('buildComputeInstanceCreatePayload — disk storage tiers', () => {
  it('sends the boot disk tier id and name', () => {
    const values = baseValues();
    values.spec.bootDisk = { sizeGib: '20', storageTier: { id: 'id-balanced', name: 'balanced' } };

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({
      sizeGib: 20,
      storageTier: { id: 'id-balanced', name: 'balanced' },
    });
  });

  it('omits storage_tier from the boot disk when no tier is selected', () => {
    const values = baseValues();
    values.spec.bootDisk = { sizeGib: '20', storageTier: emptyResourceSelectValue() };

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({ sizeGib: 20 });
    expect(payload.spec?.bootDisk).not.toHaveProperty('storageTier');
  });

  it('carries both the boot disk tier and each additional disk tier', () => {
    const values = baseValues();
    values.spec.bootDisk = { sizeGib: '20', storageTier: { id: 'id-fast', name: 'fast' } };
    values.spec.additionalDisks = [
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
    ];

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({
      sizeGib: 20,
      storageTier: { id: 'id-fast', name: 'fast' },
    });
    expect(payload.spec?.additionalDisks).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });

  it('drops additional disk rows that have no size', () => {
    const values = baseValues();
    values.spec.bootDisk = { sizeGib: '20', storageTier: emptyResourceSelectValue() };
    values.spec.additionalDisks = [
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
      { sizeGib: '', storageTier: { id: 'id-fast', name: 'fast' } },
    ];

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.additionalDisks).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });

  it('omits additional_disks entirely when no rows have a size', () => {
    const values = baseValues();
    values.spec.additionalDisks = [{ sizeGib: '', storageTier: { id: 'id-fast', name: 'fast' } }];

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('additionalDisks');
  });

  it('sends an explicit empty additional_disks array when the catalog default was cleared', () => {
    const values = baseValues();
    values.spec.additionalDisks = [];

    const payload = buildComputeInstanceCreatePayload(
      values,
      catalogItemWithAdditionalDisksDefault,
    );

    expect(payload.spec?.additionalDisks).toEqual([]);
  });

  it('still sends non-empty additional disks as usual when the catalog defines a default', () => {
    const values = baseValues();
    values.spec.additionalDisks = [
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
    ];

    const payload = buildComputeInstanceCreatePayload(
      values,
      catalogItemWithAdditionalDisksDefault,
    );

    expect(payload.spec?.additionalDisks).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });
});
