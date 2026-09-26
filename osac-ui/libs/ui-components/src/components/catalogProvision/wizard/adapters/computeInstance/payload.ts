import { type MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceSchema } from '@osac/types';
import { ComputeInstanceRunStrategy } from '@osac/types';

import type { ComputeInstanceWizardValues } from './fields';
import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../../../Form/resourceSelectValue';
import { getCatalogFieldOverlay, readCatalogFieldDefinitions } from '../../catalogOverlay';

export const createEmptyComputeInstanceValues = (): ComputeInstanceWizardValues => ({
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
});

const tierField = (
  storageTier: ResourceSelectValue,
): { storageTier?: { id?: string; name?: string } } => {
  const id = storageTier?.id?.trim() ?? '';
  const name = storageTier?.name?.trim() ?? '';
  if (!id && !name) {
    return {};
  }
  return {
    storageTier: {
      ...(id ? { id } : {}),
      ...(name ? { name } : {}),
    },
  };
};

export const buildComputeInstanceCreatePayload = (
  values: ComputeInstanceWizardValues,
  catalogItem: ComputeInstanceCatalogItem,
): MessageInitShape<typeof ComputeInstanceSchema> => {
  const spec: MessageInitShape<typeof ComputeInstanceSchema>['spec'] = {
    catalogItem: {
      id: catalogItem.id,
    },
    instanceType: {
      id: values.spec.instanceType,
    },
    runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS,
    networkAttachments: [
      {
        subnet: {
          id: values.spec.networking.subnet,
        },
        securityGroups: values.spec.networking.securityGroups.map((id) => ({ id })),
      },
    ],
  };

  const sshKeyName = values.spec.sshKey.name.trim();
  if (sshKeyName) {
    spec.sshKey = { name: sshKeyName };
  }

  const userData = values.spec.userData.trim();
  if (userData) {
    spec.userData = userData;
  }

  const bootDiskRaw = values.spec.bootDisk.sizeGib.trim();
  if (bootDiskRaw) {
    spec.bootDisk = {
      sizeGib: Number(bootDiskRaw),
      ...tierField(values.spec.bootDisk.storageTier),
    };
  }

  const additionalDisks = values.spec.additionalDisks
    .filter((disk) => disk.sizeGib.trim())
    .map((disk) => ({ sizeGib: Number(disk.sizeGib.trim()), ...tierField(disk.storageTier) }));
  const hasAdditionalDisksDefault =
    getCatalogFieldOverlay('spec.additional_disks', readCatalogFieldDefinitions(catalogItem), '')
      .defaultValue !== undefined;
  if (additionalDisks.length > 0 || hasAdditionalDisksDefault) {
    spec.additionalDisks = additionalDisks;
  }

  return {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec,
  };
};
