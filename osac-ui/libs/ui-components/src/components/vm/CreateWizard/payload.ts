import { type MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceSchema } from '@osac/types';
import { ComputeInstanceRunStrategy } from '@osac/types';

import type { ComputeInstanceWizardValues } from './fields';
import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
} from '../../catalog/computeCatalogItemResourceDisplay';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  boolFieldFromPolicy,
  catalogAttachmentFromPolicy,
  networkAttachmentFieldsFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachmentPolicy';
import {
  provisioningFieldFromPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export const createEmptyComputeInstanceValues = (): ComputeInstanceWizardValues => ({
  catalogItemId: '',
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
      useDefaultNetwork: provisioningFieldValue(true),
      virtualNetwork: emptyResourceSelectValue(),
      subnet: emptyResourceSelectValue(),
      securityGroups: [],
      autoExternalIpAttachment: provisioningFieldValue(false),
    },
  },
});

export const getComputeInstanceInitialValues = (
  catalogItem?: ComputeInstanceCatalogItem,
): ComputeInstanceWizardValues => {
  const values = createEmptyComputeInstanceValues();
  if (!catalogItem) {
    return values;
  }
  const fields = catalogItem.fields;
  const stringValue = (value: string | number | undefined): string =>
    value === undefined ? '' : String(value);
  const sizeGib =
    fields?.bootDisk?.sizeGib?.behavior.case === 'editable'
      ? fields.bootDisk.sizeGib.behavior.value.defaultValue
      : fields?.bootDisk?.sizeGib?.behavior.case === 'locked'
        ? fields.bootDisk.sizeGib.behavior.value
        : undefined;
  const storageTier =
    fields?.bootDisk?.storageTier?.behavior.case === 'editable'
      ? fields.bootDisk.storageTier.behavior.value.defaultValue
      : fields?.bootDisk?.storageTier?.behavior.case === 'locked'
        ? fields.bootDisk.storageTier.behavior.value
        : undefined;
  const tier =
    typeof storageTier === 'string'
      ? { id: '', name: storageTier }
      : storageTier && typeof storageTier === 'object' && !Array.isArray(storageTier)
        ? {
            id:
              typeof (storageTier as { id?: unknown }).id === 'string'
                ? (storageTier as { id: string }).id
                : '',
            name:
              typeof (storageTier as { name?: unknown }).name === 'string'
                ? (storageTier as { name: string }).name
                : '',
          }
        : emptyResourceSelectValue();
  const additional =
    fields?.additionalDisks?.behavior.case === 'editable'
      ? fields.additionalDisks.behavior.value.defaultValue
      : fields?.additionalDisks?.behavior.case === 'locked'
        ? fields.additionalDisks.behavior.value
        : undefined;
  const sshKeyName =
    catalogItem.fields?.sshKey?.behavior?.case === 'locked'
      ? catalogItem.fields.sshKey.behavior.value.name
      : catalogItem.fields?.sshKey?.behavior?.case === 'editable'
        ? (catalogItem.fields.sshKey.behavior.value.defaultValue?.name ?? '')
        : '';
  const userData =
    fields?.userData?.behavior.case === 'editable'
      ? (fields.userData.behavior.value.defaultValue ?? '')
      : fields?.userData?.behavior.case === 'locked'
        ? fields.userData.behavior.value
        : '';
  const instanceTypeRef = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const instanceTypeValue = instanceTypeRef?.id?.trim() || instanceTypeRef?.name?.trim() || '';
  const diskImageRef = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const diskImageValue =
    !diskImageRef?.id && !diskImageRef?.name
      ? emptyResourceSelectValue()
      : { id: diskImageRef.id, name: diskImageRef.name };
  return {
    ...values,
    catalogItemId: catalogItem.id,
    spec: {
      ...values.spec,
      userData: provisioningFieldFromPolicy(userData, fields?.userData),
      sshKey: provisioningFieldFromPolicy(sshKeyName, catalogItem.fields?.sshKey),
      diskImage: provisioningFieldFromPolicy(diskImageValue, fields?.diskImage),
      instanceType: provisioningFieldFromPolicy(instanceTypeValue, fields?.instanceType),
      bootDisk: {
        sizeGib: provisioningFieldFromPolicy(stringValue(sizeGib), fields?.bootDisk?.sizeGib),
        storageTier: provisioningFieldFromPolicy(tier, fields?.bootDisk?.storageTier),
      },
      additionalDisks: provisioningFieldFromPolicy(
        additional?.items.map((entry) => ({
          sizeGib: String(entry.sizeGib ?? ''),
          storageTier: entry.storageTier
            ? { id: entry.storageTier.id, name: entry.storageTier.name }
            : emptyResourceSelectValue(),
        })) ?? [],
        fields?.additionalDisks,
      ),
      networking: {
        ...networkAttachmentFieldsFromPolicy(
          fields?.networkAttachments,
          catalogAttachmentFromPolicy(fields?.networkAttachments)?.items[0],
        ),
        autoExternalIpAttachment: boolFieldFromPolicy(fields?.autoExternalIpAttachment),
      },
    },
  };
};

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
  };

  if (catalogItem.fields?.runStrategy?.behavior.case !== 'locked') {
    spec.runStrategy = ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS;
  }

  if (!values.spec.instanceType.locked && values.spec.instanceType.value) {
    spec.instanceType = {
      id: values.spec.instanceType.value,
    };
  }

  if (!values.spec.networking.useDefaultNetwork.locked) {
    if (values.spec.networking.useDefaultNetwork.value) {
      spec.networkAttachments = [];
    } else {
      const subnetId = values.spec.networking.subnet.id.trim();
      if (subnetId) {
        spec.networkAttachments = [
          {
            subnet: { id: subnetId },
            securityGroups: values.spec.networking.securityGroups
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

  if (values.spec.userDataSource.value === 'secret') {
    const userDataSecretName = values.spec.userDataSecret.value.trim();
    if (!values.spec.userDataSecret.locked && userDataSecretName) {
      spec.userDataSecret = { name: userDataSecretName };
    }
  } else {
    const userData = values.spec.userData.value.trim();
    if (!values.spec.userData.locked && userData) {
      spec.userData = userData;
    }
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
      catalogItem.fields?.additionalDisks?.behavior.case !== undefined;
    if (additionalDisks.length > 0 || hasAdditionalDisksDefault) {
      spec.additionalDisks = additionalDisks;
    }
  }

  return {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec,
  };
};
