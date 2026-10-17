import { BareMetalInstanceCatalogItem } from '@osac/types';

import { bareMetalDiskImageReferenceFromPolicy } from '../../catalog/bareMetalCatalogItemResourceDisplay';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  boolFieldFromPolicy,
  catalogAttachmentFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachmentPolicy';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  provisioningFieldFromPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export const BM_SSH_KEY_FORM_PATH = 'spec.sshKey';

export const BM_USER_DATA_FORM_PATH = 'spec.userData';
export const BM_USER_DATA_SOURCE_FORM_PATH = 'spec.userDataSource';
export const BM_USER_DATA_SECRET_FORM_PATH = 'spec.userDataSecret';

export const BM_INSTANCE_TYPE_FORM_PATH = 'spec.instanceType';

export const BM_DISK_IMAGE_FORM_PATH = 'spec.diskImage';

export interface BareMetalNetworkAttachmentRow {
  id: string;
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
}

export interface BareMetalNetworkAttachmentUnit {
  useDefaults: boolean;
  attachments: BareMetalNetworkAttachmentRow[];
}

export interface BareMetalNetworkingFormValues {
  networkAttachment: ProvisioningFieldValue<BareMetalNetworkAttachmentUnit>;
  attachExternalIp: ProvisioningFieldValue<boolean>;
}

export interface BareMetalInstanceWizardValues {
  catalogItemId: string;
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
    networking: BareMetalNetworkingFormValues;
  };
}

export const hasBareMetalAuthentication = (
  sshKey: string | undefined,
  userData: string | undefined,
  userDataSecretName?: string,
): boolean => Boolean(sshKey?.trim() || userData?.trim() || userDataSecretName?.trim());

let nextAttachmentId = 1;

export const createNetworkAttachmentRowId = (): string => {
  return `attachment-${nextAttachmentId++}`;
};

export const createEmptyNetworkAttachmentRow = (): BareMetalNetworkAttachmentRow => ({
  id: createNetworkAttachmentRowId(),
  virtualNetwork: emptyResourceSelectValue(),
  subnet: emptyResourceSelectValue(),
  securityGroups: [],
});

export const createEmptyBareMetalNetworkingValues = (): BareMetalNetworkingFormValues => ({
  networkAttachment: provisioningFieldValue({
    useDefaults: true,
    attachments: [createEmptyNetworkAttachmentRow()],
  }),
  attachExternalIp: provisioningFieldValue(false),
});

const resourceSelectFromReference = (reference?: {
  id?: string;
  name?: string;
}): ResourceSelectValue => {
  const id = reference?.id?.trim() ?? '';
  const name = reference?.name?.trim() ?? '';
  return id || name ? { id, name } : emptyResourceSelectValue();
};

export const getBareMetalNetworkingFormValues = (
  catalogItem?: BareMetalInstanceCatalogItem,
): BareMetalNetworkingFormValues => {
  if (!catalogItem) {
    return createEmptyBareMetalNetworkingValues();
  }

  const policy = catalogItem.fields?.networkAttachments;
  const attachment = catalogAttachmentFromPolicy(policy)?.items[0];
  const subnet = resourceSelectFromReference(attachment?.subnet);
  const hasCustomAttachment = Boolean(subnet.id || subnet.name);

  return {
    networkAttachment: provisioningFieldFromPolicy(
      {
        useDefaults: !hasCustomAttachment,
        attachments: [
          {
            ...createEmptyNetworkAttachmentRow(),
            subnet: hasCustomAttachment ? subnet : emptyResourceSelectValue(),
            securityGroups: (attachment?.securityGroups ?? []).map((group) =>
              resourceSelectFromReference(group),
            ),
          },
        ],
      },
      policy,
    ),
    attachExternalIp: boolFieldFromPolicy(catalogItem.fields?.autoExternalIpAttachment),
  };
};

export const createEmptyBareMetalInstanceValues = (): BareMetalInstanceWizardValues => ({
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshKey: provisioningFieldValue(''),
    userDataSource: provisioningFieldValue('inline'),
    userData: provisioningFieldValue(''),
    userDataSecret: provisioningFieldValue(''),
    instanceType: provisioningFieldValue(''),
    diskImage: provisioningFieldValue(emptyResourceSelectValue()),
    networking: createEmptyBareMetalNetworkingValues(),
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
    catalogItemId: catalogItem.id,
    spec: {
      ...values.spec,
      sshKey: provisioningFieldFromPolicy(
        catalogItem.fields?.sshPublicKey?.behavior.case === 'editable'
          ? (catalogItem.fields.sshPublicKey.behavior.value.defaultValue ?? '')
          : catalogItem.fields?.sshPublicKey?.behavior.case === 'locked'
            ? catalogItem.fields.sshPublicKey.behavior.value
            : '',
        catalogItem.fields?.sshPublicKey,
      ),
      userData: provisioningFieldFromPolicy(
        catalogItem.fields?.userData?.behavior.case === 'editable'
          ? (catalogItem.fields.userData.behavior.value.defaultValue ?? '')
          : catalogItem.fields?.userData?.behavior.case === 'locked'
            ? catalogItem.fields.userData.behavior.value
            : '',
        catalogItem.fields?.userData,
      ),
      instanceType: provisioningFieldFromPolicy(
        catalogItem.fields?.instanceType?.behavior.case === 'editable'
          ? (catalogItem.fields.instanceType.behavior.value.defaultValue?.name ?? '')
          : catalogItem.fields?.instanceType?.behavior.case === 'locked'
            ? catalogItem.fields.instanceType.behavior.value.name
            : '',
        catalogItem.fields?.instanceType,
      ),
      diskImage: provisioningFieldFromPolicy(
        diskImageSelectValueFromCatalog(catalogItem),
        catalogItem.fields?.diskImage,
      ),
      networking: getBareMetalNetworkingFormValues(catalogItem),
    },
  };
};
