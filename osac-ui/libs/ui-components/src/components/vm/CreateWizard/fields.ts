import { type ComputeInstanceCatalogItem, GuestOSFamily } from '@osac/types';

import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  type NetworkAttachmentFormFields,
  getEmptyNetworkAttachmentFormFields,
  networkAttachmentFieldsFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachments';
import type {
  ProvisioningFieldValue,
  UserDataSourceFormField,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  getFieldFromCatalogItemPolicy,
  getUserDataSourceFieldsFromPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface ComputeInstanceNetworkingValues {
  networkAttachment: ProvisioningFieldValue<NetworkAttachmentFormFields>;
  autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
}

export interface ComputeInstanceDiskValues {
  sizeGib: string;
  storageTier: ResourceSelectValue;
}

interface ComputeInstanceDiskListPolicyValue {
  items: Array<{
    sizeGib?: number;
    storageTier?: ResourceSelectValue;
  }>;
}

export interface ComputeInstanceWizardValues {
  catalogItemReference: { name: string; id: string };
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshKey: ProvisioningFieldValue<string>;
    guestOsFamily: GuestOSFamily;
    diskImage: ProvisioningFieldValue<ResourceSelectValue>;
    instanceType: ProvisioningFieldValue<ResourceSelectValue>;
    userData: UserDataSourceFormField;
    bootDisk: {
      sizeGib: ProvisioningFieldValue<string>;
      storageTier: ProvisioningFieldValue<ResourceSelectValue>;
    };
    additionalDisks: ProvisioningFieldValue<ComputeInstanceDiskValues[]>;
    networking: ComputeInstanceNetworkingValues;
  };
}

export const createEmptyComputeInstanceValues = (): ComputeInstanceWizardValues => ({
  catalogItemReference: { name: '', id: '' },
  metadata: { name: '', project: '' },
  spec: {
    sshKey: provisioningFieldValue(''),
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    diskImage: provisioningFieldValue(emptyResourceSelectValue()),
    instanceType: provisioningFieldValue(emptyResourceSelectValue()),
    userData: provisioningFieldValue({ source: 'inline', inline: '', secret: '' }),
    bootDisk: {
      sizeGib: provisioningFieldValue(''),
      storageTier: provisioningFieldValue(emptyResourceSelectValue()),
    },
    additionalDisks: provisioningFieldValue([]),
    networking: {
      networkAttachment: provisioningFieldValue(getEmptyNetworkAttachmentFormFields()),
      autoExternalIpAttachment: provisioningFieldValue(false),
    },
  },
});

export const VM_SSH_KEY_FORM_PATH = 'spec.sshKey';
export const VM_DISK_IMAGE_FORM_PATH = 'spec.diskImage';
export const VM_USER_DATA_FORM_PATH = 'spec.userData';

export const getComputeInstanceNetworkAttachment = (
  catalogItem?: ComputeInstanceCatalogItem,
): ProvisioningFieldValue<NetworkAttachmentFormFields> => {
  if (!catalogItem) {
    return provisioningFieldValue(getEmptyNetworkAttachmentFormFields());
  }

  return networkAttachmentFieldsFromPolicy(catalogItem.fields?.networkAttachments);
};

export const getComputeInstanceInitialValues = (
  catalogItem?: ComputeInstanceCatalogItem,
): ComputeInstanceWizardValues => {
  const values = createEmptyComputeInstanceValues();
  if (!catalogItem) {
    return values;
  }
  return {
    ...values,
    catalogItemReference: {
      name: catalogItem.metadata?.name || '',
      id: catalogItem.id,
    },
    spec: {
      ...values.spec,
      userData: getUserDataSourceFieldsFromPolicy(catalogItem.fields?.userDataSource),
      sshKey: (() => {
        const field = getFieldFromCatalogItemPolicy<{ name: string }>(catalogItem.fields?.sshKey, {
          name: '',
        });
        return provisioningFieldValue(field.value.name ?? '', field.locked);
      })(),
      diskImage: getFieldFromCatalogItemPolicy(
        catalogItem.fields?.diskImage,
        emptyResourceSelectValue(),
      ),
      instanceType: (() => {
        const field = getFieldFromCatalogItemPolicy<ResourceSelectValue>(
          catalogItem.fields?.instanceType,
          emptyResourceSelectValue(),
        );
        return provisioningFieldValue(
          { id: field.value.id || '', name: field.value.name || '' },
          field.locked,
        );
      })(),
      bootDisk: {
        sizeGib: (() => {
          const field = getFieldFromCatalogItemPolicy(catalogItem.fields?.bootDisk?.sizeGib, 0);
          return provisioningFieldValue(String(field.value || ''), field.locked);
        })(),
        storageTier: getFieldFromCatalogItemPolicy(
          catalogItem.fields?.bootDisk?.storageTier,
          emptyResourceSelectValue(),
        ),
      },
      additionalDisks: (() => {
        const field = getFieldFromCatalogItemPolicy<ComputeInstanceDiskListPolicyValue>(
          catalogItem.fields?.additionalDisks,
          { items: [] },
        );
        return provisioningFieldValue(
          field.value.items.map((entry) => ({
            sizeGib: String(entry.sizeGib ?? ''),
            storageTier: entry.storageTier
              ? { id: entry.storageTier.id, name: entry.storageTier.name }
              : emptyResourceSelectValue(),
          })),
          field.locked,
        );
      })(),
      networking: {
        networkAttachment: getComputeInstanceNetworkAttachment(catalogItem),
        autoExternalIpAttachment: getFieldFromCatalogItemPolicy(
          catalogItem.fields?.autoExternalIpAttachment,
          false,
        ),
      },
    },
  };
};

export const CONFIGURATION_CATALOG_PATHS = [
  'spec.user_data_source',
  'spec.boot_disk.size_gib',
  'spec.boot_disk.storage_tier',
] as const;
