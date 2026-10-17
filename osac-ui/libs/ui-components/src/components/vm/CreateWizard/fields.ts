import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface ComputeInstanceNetworkingValues {
  useDefaultNetwork: ProvisioningFieldValue<boolean>;
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
  autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
}

export interface ComputeInstanceDiskValues {
  sizeGib: string;
  storageTier: ResourceSelectValue;
}

export type UserDataSource = 'inline' | 'secret';

export interface ComputeInstanceWizardValues {
  catalogItemId: string;
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshKey: ProvisioningFieldValue<string>;
    diskImage: ProvisioningFieldValue<ResourceSelectValue>;
    instanceType: ProvisioningFieldValue<string>;
    userDataSource: ProvisioningFieldValue<UserDataSource>;
    userData: ProvisioningFieldValue<string>;
    userDataSecret: ProvisioningFieldValue<string>;
    bootDisk: {
      sizeGib: ProvisioningFieldValue<string>;
      storageTier: ProvisioningFieldValue<ResourceSelectValue>;
    };
    additionalDisks: ProvisioningFieldValue<ComputeInstanceDiskValues[]>;
    networking: ComputeInstanceNetworkingValues;
  };
}

export const VM_SSH_KEY_FORM_PATH = 'spec.sshKey';
export const VM_DISK_IMAGE_FORM_PATH = 'spec.diskImage';
export const VM_USER_DATA_SOURCE_FORM_PATH = 'spec.userDataSource';
export const VM_USER_DATA_FORM_PATH = 'spec.userData';
export const VM_USER_DATA_SECRET_FORM_PATH = 'spec.userDataSecret';

export const CONFIGURATION_CATALOG_PATHS = [
  'spec.user_data',
  'spec.boot_disk.size_gib',
  'spec.boot_disk.storage_tier',
] as const;
