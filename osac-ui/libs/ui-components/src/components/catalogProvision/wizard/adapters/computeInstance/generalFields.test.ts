import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { applyVmCatalogGeneralDefaults } from './applyCatalogGeneralDefaults';
import { buildComputeInstanceCreatePayload, createEmptyComputeInstanceValues } from './payload';

const catalogItemWithSshKeyPolicy = (behavior: unknown): ComputeInstanceCatalogItem =>
  ({
    id: 'catalog-item',
    fields: { sshKey: { behavior } },
  }) as unknown as ComputeInstanceCatalogItem;

describe('applyVmCatalogGeneralDefaults', () => {
  it('prefills a locked SSH Secret reference from the catalog', () => {
    const setFieldValue = vi.fn();

    applyVmCatalogGeneralDefaults(
      catalogItemWithSshKeyPolicy({
        case: 'locked',
        value: { id: 'ssh-secret-id', name: 'locked-key' },
      }),
      { setFieldValue } as never,
    );

    expect(setFieldValue).toHaveBeenCalledWith('spec.sshKey.name', 'locked-key');
  });

  it('prefills an editable SSH Secret default from the catalog', () => {
    const setFieldValue = vi.fn();

    applyVmCatalogGeneralDefaults(
      catalogItemWithSshKeyPolicy({
        case: 'editable',
        value: { defaultValue: { id: 'ssh-secret-id', name: 'default-key' } },
      }),
      { setFieldValue } as never,
    );

    expect(setFieldValue).toHaveBeenCalledWith('spec.sshKey.name', 'default-key');
  });
});

describe('buildComputeInstanceCreatePayload SSH key', () => {
  const buildValues = (sshKeyName: string) => ({
    ...createEmptyComputeInstanceValues(),
    catalogItemId: 'catalog-item',
    metadata: { name: 'web-01', project: 'project-a' },
    spec: {
      ...createEmptyComputeInstanceValues().spec,
      sshKey: { name: sshKeyName },
      networking: {
        virtualNetwork: 'vn-1',
        subnet: 'subnet-1',
        securityGroups: ['sg-1'],
      },
    },
  });

  it('sends the selected SSH Secret reference', () => {
    const vm = buildComputeInstanceCreatePayload(buildValues('tenant-ssh-key'), {
      id: 'catalog-item',
    } as ComputeInstanceCatalogItem);

    expect(vm.spec?.sshKey).toEqual({ name: 'tenant-ssh-key' });
  });

  it('omits the SSH Secret reference when none is selected', () => {
    const vm = buildComputeInstanceCreatePayload(buildValues(''), {
      id: 'catalog-item',
    } as ComputeInstanceCatalogItem);

    expect(vm.spec?.sshKey).toBeUndefined();
  });
});
