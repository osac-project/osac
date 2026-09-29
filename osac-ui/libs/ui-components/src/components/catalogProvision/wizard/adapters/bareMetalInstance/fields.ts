import { FormikHelpers } from 'formik';
import { TFunction } from 'i18next';

import { BareMetalInstanceCatalogItem } from '@osac/types';

import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../../../Form/ResourceSelectField';
import {
  getCatalogFieldOverlay,
  overlayDefaultToFormValue,
  readCatalogFieldDefinitions,
} from '../../catalogOverlay';

export const BM_SSH_KEY_WIRE_PATH = 'ssh_public_key';
export const BM_SSH_KEY_FORM_PATH = 'spec.sshKey';

export const BM_USER_DATA_WIRE_PATH = 'user_data';
export const BM_USER_DATA_FORM_PATH = 'spec.userData';

export const BM_INSTANCE_TYPE_WIRE_PATH = 'instance_type.name';
export const BM_INSTANCE_TYPE_FORM_PATH = 'spec.instanceType.name';

export const BM_NETWORK_ATTACHMENTS_WIRE_PATH = 'network_attachments';
export const BM_AUTO_EXTERNAL_IP_WIRE_PATH = 'auto_external_ip_attachment';

export interface BareMetalNetworkAttachmentRow {
  id: string;
  virtualNetwork: string;
  subnet: string;
  securityGroups: string[];
}

export interface BareMetalNetworkingFormValues {
  useDefaults: boolean;
  attachments: BareMetalNetworkAttachmentRow[];
  attachExternalIp: boolean;
}

export interface BareMetalInstanceWizardValues {
  catalogItemId: string;
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshKey: string;
    userData: string;
    instanceType: {
      name: string;
    };
    diskImage: ResourceSelectValue;
    networking: BareMetalNetworkingFormValues;
  };
}

export const hasBareMetalAuthentication = (
  sshKey: string | undefined,
  userData: string | undefined,
): boolean => Boolean(sshKey?.trim() || userData?.trim());

let nextAttachmentId = 1;

export const createNetworkAttachmentRowId = (): string => {
  return `attachment-${nextAttachmentId++}`;
};

export const createEmptyNetworkAttachmentRow = (): BareMetalNetworkAttachmentRow => ({
  id: createNetworkAttachmentRowId(),
  virtualNetwork: '',
  subnet: '',
  securityGroups: [],
});

export const createEmptyBareMetalNetworkingValues = (): BareMetalNetworkingFormValues => ({
  useDefaults: true,
  attachments: [createEmptyNetworkAttachmentRow()],
  attachExternalIp: false,
});

export const createEmptyBareMetalInstanceValues = (): BareMetalInstanceWizardValues => ({
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshKey: '',
    userData: '',
    instanceType: {
      name: '',
    },
    diskImage: emptyResourceSelectValue(),
    networking: createEmptyBareMetalNetworkingValues(),
  },
});

export const applyBmCatalogDefaults = (
  catalogItem: BareMetalInstanceCatalogItem,
  helpers: FormikHelpers<BareMetalInstanceWizardValues>,
  t: TFunction,
): void => {
  const definitions = readCatalogFieldDefinitions(catalogItem);

  const sshKeyOverlay = getCatalogFieldOverlay(
    BM_SSH_KEY_WIRE_PATH,
    definitions,
    t('SSH public key'),
  );
  const userDataOverlay = getCatalogFieldOverlay(
    BM_USER_DATA_WIRE_PATH,
    definitions,
    t('User data'),
  );

  const instanceTypeOverlay = getCatalogFieldOverlay(
    BM_INSTANCE_TYPE_WIRE_PATH,
    definitions,
    t('Instance type'),
  );

  const sshDefault = overlayDefaultToFormValue(sshKeyOverlay);
  if (sshDefault !== undefined) {
    void helpers.setFieldValue(BM_SSH_KEY_FORM_PATH, sshDefault);
  }

  const userDataDefault = overlayDefaultToFormValue(userDataOverlay);
  if (userDataDefault !== undefined) {
    void helpers.setFieldValue(BM_USER_DATA_FORM_PATH, userDataDefault);
  }

  const instanceTypeDefault = overlayDefaultToFormValue(instanceTypeOverlay);
  if (instanceTypeDefault !== undefined) {
    void helpers.setFieldValue(BM_INSTANCE_TYPE_FORM_PATH, instanceTypeDefault);
  }
};
