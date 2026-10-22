import { describe, expect, it } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { buildComputeInstanceCreatePayload, createEmptyComputeInstanceValues } from './payload';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

describe('buildComputeInstanceCreatePayload SSH key', () => {
  const buildValues = (sshKeyName: string) => ({
    ...createEmptyComputeInstanceValues(),
    catalogItemReference: { name: 'catalog-item', id: 'catalog-item' },
    metadata: { name: 'web-01', project: 'project-a' },
    spec: {
      ...createEmptyComputeInstanceValues().spec,
      sshKey: { value: sshKeyName, locked: false },
      networking: {
        networkAttachment: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: 'vn-1', name: 'vn-1' },
          subnet: { id: 'subnet-1', name: 'subnet-1' },
          securityGroups: [{ id: 'sg-1', name: 'sg-1' }],
        }),
        autoExternalIpAttachment: provisioningFieldValue(false),
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
