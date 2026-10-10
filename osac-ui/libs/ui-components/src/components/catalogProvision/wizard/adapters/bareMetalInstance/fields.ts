import { FormikHelpers } from 'formik';
import { TFunction } from 'i18next';

import { BareMetalInstanceCatalogItem } from '@osac/types';

import type { ResourceSelectValue } from '../../../../Form/resourceSelectValue';
import { emptyResourceSelectValue } from '../../../../Form/resourceSelectValue';
import {
  getCatalogFieldOverlay,
  overlayDefaultToFormValue,
  readCatalogFieldDefinitions,
} from '../../catalogOverlay';

export const BM_SSH_KEY_WIRE_PATH = 'ssh_public_key';
export const BM_SSH_KEY_FORM_PATH = 'spec.sshKey';

export const BM_USER_DATA_WIRE_PATH = 'user_data';
export const BM_USER_DATA_FORM_PATH = 'spec.userData';
export const BM_USER_DATA_SOURCE_FORM_PATH = 'spec.userDataSource';
export const BM_USER_DATA_SECRET_FORM_PATH = 'spec.userDataSecret.name';

export const BM_INSTANCE_TYPE_WIRE_PATH = 'instance_type.name';
export const BM_INSTANCE_TYPE_FORM_PATH = 'spec.instanceType.name';

export const BM_NETWORK_ATTACHMENTS_WIRE_PATH = 'network_attachments';
export const BM_AUTO_EXTERNAL_IP_WIRE_PATH = 'auto_external_ip_attachment';

export interface BareMetalNetworkAttachmentRow {
  id: string;
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
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
    userDataSource: 'inline' | 'secret';
    userData: string;
    userDataSecret: {
      name: string;
    };
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
  useDefaults: true,
  attachments: [createEmptyNetworkAttachmentRow()],
  attachExternalIp: false,
});

export const createEmptyBareMetalInstanceValues = (): BareMetalInstanceWizardValues => ({
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshKey: '',
    userDataSource: 'inline',
    userData: '',
    userDataSecret: { name: '' },
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
