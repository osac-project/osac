import { type MessageInitShape } from '@bufbuild/protobuf';

import { ComputeInstanceSchema } from '@osac/types';
import { ComputeInstanceRunStrategy } from '@osac/types';
import type { ComputeInstanceCatalogItem } from '@osac/types';

import { type ComputeInstanceWizardValues } from './fields';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  getUserDataSourceFieldsFromPolicy,
  getUserDataSourcePayload,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export { createEmptyComputeInstanceValues } from './fields';

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
  catalogItem?: ComputeInstanceCatalogItem,
): MessageInitShape<typeof ComputeInstanceSchema> => {
  const spec: MessageInitShape<typeof ComputeInstanceSchema>['spec'] = {
    catalogItem: {
      id: values.catalogItemReference.id,
    },
  };

  if (catalogItem?.fields?.runStrategy?.behavior.case !== 'locked') {
    spec.runStrategy = ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS;
  }

  const instanceTypeId = values.spec.instanceType.value.id.trim();
  if (!values.spec.instanceType.locked && instanceTypeId) {
    spec.instanceType = {
      id: instanceTypeId,
    };
  }

  const networkAttachment = values.spec.networking.networkAttachment;
  if (!networkAttachment.locked) {
    if (networkAttachment.value.useDefaultNetwork) {
      spec.networkAttachments = [];
    } else {
      const subnetId = networkAttachment.value.subnet.id.trim();
      if (subnetId) {
        spec.networkAttachments = [
          {
            subnet: { id: subnetId },
            securityGroups: networkAttachment.value.securityGroups
              .filter((sg) => sg.id.trim())
              .map((sg) => ({ id: sg.id })),
          },
        ];
      }
    }
  }

  if (!values.spec.networking.autoExternalIpAttachment.locked) {
    spec.autoExternalIpAttachment = values.spec.networking.autoExternalIpAttachment.value;
  }

  const sshKeyName = values.spec.sshKey.value.trim();
  if (!values.spec.sshKey.locked && sshKeyName) {
    spec.sshKey = { name: sshKeyName };
  }

  if (!values.spec.diskImage.locked) {
    const diskImageId = values.spec.diskImage.value.id.trim();
    if (diskImageId) {
      spec.diskImage = { id: diskImageId };
    }
  }

  const initialUserData = getUserDataSourceFieldsFromPolicy(catalogItem?.fields?.userDataSource);
  const userDataSource = getUserDataSourcePayload(values.spec.userData, initialUserData);
  if (userDataSource) {
    spec.userDataSource = userDataSource;
  }

  const bootDisk: { sizeGib?: number; storageTier?: { id?: string; name?: string } } = {};
  const bootDiskRaw = values.spec.bootDisk.sizeGib.value.trim();
  if (!values.spec.bootDisk.sizeGib.locked && bootDiskRaw) {
    bootDisk.sizeGib = Number(bootDiskRaw);
  }
  if (!values.spec.bootDisk.storageTier.locked) {
    Object.assign(bootDisk, tierField(values.spec.bootDisk.storageTier.value));
  }
  if (bootDisk.sizeGib !== undefined || bootDisk.storageTier) {
    spec.bootDisk = bootDisk;
  }

  if (!values.spec.additionalDisks.locked) {
    const additionalDisks = values.spec.additionalDisks.value
      .filter((disk) => disk.sizeGib.trim())
      .map((disk) => ({ sizeGib: Number(disk.sizeGib.trim()), ...tierField(disk.storageTier) }));
    const hasAdditionalDisksDefault =
      catalogItem?.fields?.additionalDisks?.behavior.case !== undefined;
    if (additionalDisks.length > 0 || hasAdditionalDisksDefault) {
      spec.additionalDisks = {
        items: additionalDisks,
      };
    }
  }

  return {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec,
  };
};
