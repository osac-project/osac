import type { BareMetalInstanceCatalogItem } from '@osac/types';

import { bareMetalDiskImageReferenceFromPolicy } from '../../catalog/bareMetalCatalogItemResourceDisplay';
import { catalogFieldPolicyLocked } from '../../catalog/catalogFieldPolicyDisplay';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  type NetworkAttachmentFormFields,
  getEmptyNetworkAttachmentFormFields,
  networkAttachmentFieldsFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachments';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export const BM_SSH_KEY_FORM_PATH = 'spec.sshKey';

export const BM_USER_DATA_FORM_PATH = 'spec.userData';
export const BM_USER_DATA_SOURCE_FORM_PATH = 'spec.userDataSource';
export const BM_USER_DATA_SECRET_FORM_PATH = 'spec.userDataSecret';

export const BM_INSTANCE_TYPE_FORM_PATH = 'spec.instanceType';

export const BM_DISK_IMAGE_FORM_PATH = 'spec.diskImage';

export interface BareMetalInstanceWizardValues {
  catalogItemReference: { name: string; id: string };
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshKey: ProvisioningFieldValue<string>;
    userDataSource: ProvisioningFieldValue<'inline' | 'secret'>;
    userData: ProvisioningFieldValue<string>;
    userDataSecret: ProvisioningFieldValue<string>;
    instanceType: ProvisioningFieldValue<string>;
    diskImage: ProvisioningFieldValue<ResourceSelectValue>;
    networkAttachment: ProvisioningFieldValue<NetworkAttachmentFormFields>;
    autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
  };
}

export const hasBareMetalAuthentication = (
  sshKey: string | undefined,
  userData: string | undefined,
  userDataSecretName?: string,
): boolean => Boolean(sshKey?.trim() || userData?.trim() || userDataSecretName?.trim());

export const getBareMetalNetworkAttachment = (catalogItem?: BareMetalInstanceCatalogItem) => {
  if (!catalogItem) {
    return networkAttachmentFieldsFromPolicy(undefined);
  }
  return networkAttachmentFieldsFromPolicy(catalogItem.fields?.networkAttachments);
};

export const createEmptyBareMetalInstanceValues = (): BareMetalInstanceWizardValues => ({
  catalogItemReference: { name: '', id: '' },
  metadata: { name: '', project: '' },
  spec: {
    sshKey: provisioningFieldValue(''),
    userDataSource: provisioningFieldValue('inline'),
    userData: provisioningFieldValue(''),
    userDataSecret: provisioningFieldValue(''),
    instanceType: provisioningFieldValue(''),
    diskImage: provisioningFieldValue(emptyResourceSelectValue()),
    networkAttachment: provisioningFieldValue(getEmptyNetworkAttachmentFormFields()),
    autoExternalIpAttachment: provisioningFieldValue(false),
  },
});

const diskImageSelectValueFromCatalog = (
  catalogItem: BareMetalInstanceCatalogItem,
): ResourceSelectValue => {
  const reference = bareMetalDiskImageReferenceFromPolicy(catalogItem.fields?.diskImage);
  if (!reference?.id && !reference?.name) {
    return emptyResourceSelectValue();
  }
  return { id: reference.id, name: reference.name };
};

export const getBareMetalInstanceInitialValues = (
  catalogItem?: BareMetalInstanceCatalogItem,
): BareMetalInstanceWizardValues => {
  const values = createEmptyBareMetalInstanceValues();
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
      sshKey: getFieldFromCatalogItemPolicy(catalogItem.fields?.sshPublicKey, ''),
      userData: getFieldFromCatalogItemPolicy(catalogItem.fields?.userData, ''),
      instanceType: (() => {
        const field = getFieldFromCatalogItemPolicy(catalogItem.fields?.instanceType, undefined);
        const reference = field.value as { id?: string; name?: string } | undefined;
        return provisioningFieldValue(reference?.name ?? '', field.locked);
      })(),
      diskImage: provisioningFieldValue(
        diskImageSelectValueFromCatalog(catalogItem),
        catalogFieldPolicyLocked(catalogItem.fields?.diskImage),
      ),
      networkAttachment: getBareMetalNetworkAttachment(catalogItem),
      autoExternalIpAttachment: getFieldFromCatalogItemPolicy(
        catalogItem.fields?.autoExternalIpAttachment,
        false,
      ),
    },
  };
};
