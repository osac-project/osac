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
    userData: string;
    bootDisk: ComputeInstanceDiskValues;
    additionalDisks: ComputeInstanceDiskValues[];
    networking: ComputeInstanceNetworkingValues;
  };
}

export const VM_SSH_KEY_FORM_PATH = 'spec.sshKey.name';

export const CONFIGURATION_CATALOG_PATHS = [
  'spec.user_data',
  'spec.boot_disk.size_gib',
  'spec.boot_disk.storage_tier',
] as const;
