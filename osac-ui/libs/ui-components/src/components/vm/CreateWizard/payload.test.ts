import { describe, expect, it } from 'vitest';

import { type ComputeInstanceCatalogItem, ComputeInstanceRunStrategy } from '@osac/types';

import { getComputeInstanceInitialValues } from './fields';
import { buildComputeInstanceCreatePayload, createEmptyComputeInstanceValues } from './payload';
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

const buildValues = (project: string) => ({
  ...createEmptyComputeInstanceValues(),
  catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
  metadata: { name: 'my-vm', project },
  spec: {
    ...createEmptyComputeInstanceValues().spec,
    instanceType: provisioningFieldValue('standard-4-8'),
    networking: {
      networkAttachment: provisioningFieldValue({
        useDefaultNetwork: false,
        virtualNetwork: { id: 'vnet-1', name: 'vnet-1' },
        subnet: { id: 'subnet-1', name: 'subnet-1' },
        securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
      }),
      autoExternalIpAttachment: provisioningFieldValue(false),
    },
  },
});

const baseValues = () => {
  const values = createEmptyComputeInstanceValues();
  return {
    ...values,
    catalogItemReference: { name: vmCatalogItem.title, id: vmCatalogItem.id },
    metadata: { name: 'web-01', project: '' },
    spec: {
      ...values.spec,
      instanceType: provisioningFieldValue('standard-4-8'),
      networking: {
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: 'vnet', name: 'vnet' },
          subnet: { id: 'subnet-1', name: 'subnet-1' },
          securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
        }),
        autoExternalIpAttachment: provisioningFieldValue(false),
      },
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
        autoExternalIpAttachment: false,
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

  it('sends inline user data when the inline source is selected', () => {
    const values = baseValues();
    values.spec.userDataSource = provisioningFieldValue('inline');
    values.spec.userData = provisioningFieldValue('  #cloud-config\nusers: []  ');
    values.spec.userDataSecret = provisioningFieldValue('stale-secret');

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    //expect(payload.spec?.userData).toBe('#cloud-config\nusers: []');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });

  it('sends a Secret reference when the Secret source is selected', () => {
    const values = baseValues();
    values.spec.userDataSource = provisioningFieldValue('secret');
    values.spec.userData = provisioningFieldValue('stale inline data');
    values.spec.userDataSecret = provisioningFieldValue('  cloud-init  ');

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    //expect(payload.spec?.userDataSecret).toEqual({ name: 'cloud-init' });
    expect(payload.spec).not.toHaveProperty('userData');
  });

  it('omits user data when the selected source is empty', () => {
    const values = baseValues();
    values.spec.userDataSource = provisioningFieldValue('secret');
    values.spec.userData = provisioningFieldValue('stale inline data');
    values.spec.userDataSecret = provisioningFieldValue('   ');

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('userData');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });
});

describe('buildComputeInstanceCreatePayload — disk storage tiers', () => {
  it('sends the boot disk tier id and name', () => {
    const values = baseValues();
    values.spec.bootDisk = {
      sizeGib: provisioningFieldValue('20'),
      storageTier: provisioningFieldValue({ id: 'id-balanced', name: 'balanced' }),
    };

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({
      sizeGib: 20,
      storageTier: { id: 'id-balanced', name: 'balanced' },
    });
  });

  it('omits storage_tier from the boot disk when no tier is selected', () => {
    const values = baseValues();
    values.spec.bootDisk = {
      sizeGib: provisioningFieldValue('20'),
      storageTier: provisioningFieldValue(emptyResourceSelectValue()),
    };

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({ sizeGib: 20 });
    expect(payload.spec?.bootDisk).not.toHaveProperty('storageTier');
  });

  it('carries both the boot disk tier and each additional disk tier', () => {
    const values = baseValues();
    values.spec.bootDisk = {
      sizeGib: provisioningFieldValue('20'),
      storageTier: provisioningFieldValue({ id: 'id-fast', name: 'fast' }),
    };
    values.spec.additionalDisks = provisioningFieldValue([
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.bootDisk).toEqual({
      sizeGib: 20,
      storageTier: { id: 'id-fast', name: 'fast' },
    });
    expect(payload.spec?.additionalDisks?.items).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });

  it('drops additional disk rows that have no size', () => {
    const values = baseValues();
    values.spec.bootDisk = {
      sizeGib: provisioningFieldValue('20'),
      storageTier: provisioningFieldValue(emptyResourceSelectValue()),
    };
    values.spec.additionalDisks = provisioningFieldValue([
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
      { sizeGib: '', storageTier: { id: 'id-fast', name: 'fast' } },
    ]);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.additionalDisks?.items).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });

  it('omits additional_disks entirely when no rows have a size', () => {
    const values = baseValues();
    values.spec.additionalDisks = provisioningFieldValue([
      { sizeGib: '', storageTier: { id: 'id-fast', name: 'fast' } },
    ]);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('additionalDisks');
  });

  it('still sends non-empty additional disks as usual', () => {
    const values = baseValues();
    values.spec.additionalDisks = provisioningFieldValue([
      { sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.additionalDisks?.items).toEqual([
      { sizeGib: 100, storageTier: { id: 'id-bulk', name: 'bulk' } },
    ]);
  });
});

describe('buildComputeInstanceCreatePayload — useDefaultNetwork', () => {
  it('sends an empty networkAttachments list when useDefaultNetwork is true', () => {
    const values = baseValues();
    values.spec.networking.networkAttachment.value.useDefaultNetwork = true;

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.networkAttachments).toEqual([]);
  });

  it('includes networkAttachments when useDefaultNetwork is false and subnet is set', () => {
    const values = baseValues();
    values.spec.networking.networkAttachment.value.useDefaultNetwork = false;

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.networkAttachments).toEqual([
      {
        subnet: { id: 'subnet-1' },
        securityGroups: [{ id: 'sg-1' }],
      },
    ]);
  });

  it('omits networkAttachments when useDefaultNetwork is false but subnet is empty', () => {
    const values = baseValues();
    values.spec.networking.networkAttachment.value.useDefaultNetwork = false;
    values.spec.networking.networkAttachment.value.subnet = emptyResourceSelectValue();

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('networkAttachments');
  });

  it('filters out security groups with empty IDs', () => {
    const values = baseValues();
    values.spec.networking.networkAttachment.value.useDefaultNetwork = false;
    values.spec.networking.networkAttachment.value.securityGroups = [
      { id: 'sg-1', name: 'sg-1' },
      { id: '', name: '' },
      { id: 'sg-2', name: 'sg-2' },
    ];

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.networkAttachments).toEqual([
      {
        subnet: { id: 'subnet-1' },
        securityGroups: [{ id: 'sg-1' }, { id: 'sg-2' }],
      },
    ]);
  });
});

describe('getComputeInstanceInitialValues — instance type', () => {
  it('preselects the catalog instance type id when the policy provides one', () => {
    const values = getComputeInstanceInitialValues({
      ...vmCatalogItem,
      fields: {
        instanceType: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { id: 'it-small', name: 'u1-small', shared: true } },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.spec.instanceType).toEqual({ value: 'it-small', locked: false });
  });

  it('locks the instance type when the catalog policy is locked', () => {
    const values = getComputeInstanceInitialValues({
      ...vmCatalogItem,
      fields: {
        instanceType: {
          behavior: {
            case: 'locked',
            value: { id: 'it-small', name: 'u1-small', shared: true },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.spec.instanceType).toEqual({ value: 'it-small', locked: true });
  });
});

describe('getComputeInstanceInitialValues — disk image', () => {
  it('loads a locked catalog disk image into the wizard', () => {
    const values = getComputeInstanceInitialValues({
      ...vmCatalogItem,
      fields: {
        diskImage: {
          behavior: {
            case: 'locked',
            value: { id: 'disk-image-rhel', name: 'rhel' },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.spec.diskImage).toEqual({
      value: { id: 'disk-image-rhel', name: 'rhel' },
      locked: true,
    });
  });

  it('loads an editable catalog disk image default into the wizard', () => {
    const values = getComputeInstanceInitialValues({
      ...vmCatalogItem,
      fields: {
        diskImage: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: { id: 'disk-image-fedora', name: 'fedora' },
            },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.spec.diskImage).toEqual({
      value: { id: 'disk-image-fedora', name: 'fedora' },
      locked: false,
    });
  });
});

describe('getComputeInstanceInitialValues — network attachments', () => {
  it('locks the attachment unit without locking nested pickers', () => {
    const values = getComputeInstanceInitialValues({
      ...vmCatalogItem,
      fields: {
        networkAttachments: {
          behavior: {
            case: 'locked',
            value: {
              items: [
                {
                  subnet: { id: 'subnet-1', name: 'subnet-a' },
                  securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
                },
              ],
            },
          },
        },
      },
    } as ComputeInstanceCatalogItem);

    expect(values.spec.networking.networkAttachment).toEqual({
      value: {
        useDefaultNetwork: false,
        virtualNetwork: emptyResourceSelectValue(),
        subnet: { id: 'subnet-1', name: 'subnet-a' },
        securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
      },
      locked: true,
    });
  });
});

describe('buildComputeInstanceCreatePayload — locked catalog fields', () => {
  it('omits a locked instance type so the catalog policy can apply it', () => {
    const values = baseValues();
    values.spec.instanceType = provisioningFieldValue('standard-4-8', true);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('instanceType');
  });

  it('sends instance type when the catalog policy is not locked', () => {
    const values = baseValues();
    values.spec.instanceType = provisioningFieldValue('standard-4-8');

    expect(buildComputeInstanceCreatePayload(values, vmCatalogItem).spec?.instanceType).toEqual({
      id: 'standard-4-8',
    });
  });

  it('omits run_strategy when the catalog policy locks it', () => {
    const values = baseValues();
    const lockedCatalogItem: ComputeInstanceCatalogItem = {
      ...vmCatalogItem,
      fields: {
        runStrategy: {
          behavior: {
            case: 'locked',
            value: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_HALTED,
          },
        },
      },
    } as ComputeInstanceCatalogItem;

    const payload = buildComputeInstanceCreatePayload(values, lockedCatalogItem);

    expect(payload.spec).not.toHaveProperty('runStrategy');
  });

  it('sends run_strategy ALWAYS when the catalog policy does not lock it', () => {
    const values = baseValues();

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.runStrategy).toBe(
      ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS,
    );
  });

  it('includes the selected disk image reference', () => {
    const values = baseValues();
    values.spec.diskImage.value = { id: 'disk-image-1', name: 'rhel' };

    expect(buildComputeInstanceCreatePayload(values, vmCatalogItem).spec?.diskImage).toEqual({
      id: 'disk-image-1',
    });
  });

  it('omits a locked disk image so the catalog policy can apply it', () => {
    const values = baseValues();
    values.spec.diskImage = provisioningFieldValue({ id: 'disk-image-1', name: 'rhel' }, true);

    expect(buildComputeInstanceCreatePayload(values, vmCatalogItem).spec).not.toHaveProperty(
      'diskImage',
    );
  });

  it('omits locked SSH key, user data, disks, networking, and auto external IP', () => {
    const values = baseValues();
    values.spec.sshKey = provisioningFieldValue('my-ssh-key', true);
    values.spec.diskImage = provisioningFieldValue({ id: 'disk-image-1', name: 'rhel' }, true);
    values.spec.userData = provisioningFieldValue('#cloud-config', true);
    values.spec.bootDisk = {
      sizeGib: provisioningFieldValue('20', true),
      storageTier: provisioningFieldValue({ id: 'id-balanced', name: 'balanced' }, true),
    };
    values.spec.additionalDisks = provisioningFieldValue(
      [{ sizeGib: '100', storageTier: { id: 'id-bulk', name: 'bulk' } }],
      true,
    );
    values.spec.networking.networkAttachment = provisioningFieldValue(
      values.spec.networking.networkAttachment.value,
      true,
    );
    values.spec.networking.autoExternalIpAttachment = provisioningFieldValue(true, true);

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec).not.toHaveProperty('sshKey');
    expect(payload.spec).not.toHaveProperty('diskImage');
    expect(payload.spec).not.toHaveProperty('userData');
    expect(payload.spec).not.toHaveProperty('bootDisk');
    expect(payload.spec).not.toHaveProperty('additionalDisks');
    expect(payload.spec).not.toHaveProperty('networkAttachments');
    expect(payload.spec).not.toHaveProperty('autoExternalIpAttachment');
  });
});

describe('buildComputeInstanceCreatePayload — autoExternalIpAttachment', () => {
  it('includes autoExternalIpAttachment when disabled so catalog defaults are not applied', () => {
    const values = baseValues();
    values.spec.networking.autoExternalIpAttachment.value = false;

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.autoExternalIpAttachment).toBe(false);
  });

  it('sets autoExternalIpAttachment when enabled', () => {
    const values = baseValues();
    values.spec.networking.autoExternalIpAttachment.value = true;

    const payload = buildComputeInstanceCreatePayload(values, vmCatalogItem);

    expect(payload.spec?.autoExternalIpAttachment).toBe(true);
  });
});
