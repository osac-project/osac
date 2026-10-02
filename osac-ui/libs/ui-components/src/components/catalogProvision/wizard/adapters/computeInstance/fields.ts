import type { ResourceSelectValue } from '../../../../Form/resourceSelectValue';

export interface ComputeInstanceNetworkingValues {
  virtualNetwork: string;
  subnet: string;
  securityGroups: string[];
}

export interface ComputeInstanceDiskValues {
  sizeGib: string;
  storageTier: ResourceSelectValue;
}

export interface ComputeInstanceWizardValues {
  catalogItemId: string;
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshKey: {
      name: string;
    };
    instanceType: string;
    userDataSource: 'inline' | 'secret';
    userData: string;
    userDataSecret: {
      name: string;
    };
    bootDisk: ComputeInstanceDiskValues;
    additionalDisks: ComputeInstanceDiskValues[];
    networking: ComputeInstanceNetworkingValues;
  };
}

export const VM_SSH_KEY_FORM_PATH = 'spec.sshKey.name';
export const VM_USER_DATA_SOURCE_FORM_PATH = 'spec.userDataSource';
export const VM_USER_DATA_FORM_PATH = 'spec.userData';
export const VM_USER_DATA_SECRET_FORM_PATH = 'spec.userDataSecret.name';

export const CONFIGURATION_CATALOG_PATHS = [
  'spec.user_data',
  'spec.boot_disk.size_gib',
  'spec.boot_disk.storage_tier',
] as const;
