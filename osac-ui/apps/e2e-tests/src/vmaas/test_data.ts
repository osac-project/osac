import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

import type { DiskImageCreateParams } from '../../../../libs/test-utils/src/disk_images.page';
import type {
  InstanceTypeGpu,
  InstanceTypeLifecycleAction,
} from '../../../../libs/test-utils/src/instance_types.page';

interface RawInstanceTypeGpu {
  count: number;
  resource_name: string;
  pci_device_selector: string;
}

interface RawInstanceType {
  name: string;
  description: string;
  vcpus: number;
  memory_gib: number;
  gpu?: RawInstanceTypeGpu;
}

interface RawDiskImage {
  name: string;
  source_type: string;
  source_ref: string;
  guest_os_family: string;
  architectures: [string, ...string[]];
  scope: string;
}

interface RawExtravars {
  instance_type: RawInstanceType;
  disk_image: RawDiskImage;
  lifecycle_actions: InstanceTypeLifecycleAction[];
  tenant_name: string;
}

export interface InstanceTypeTestData {
  name: string;
  description: string;
  vcpus: number;
  memoryGib: number;
  gpu?: InstanceTypeGpu;
  lifecycleActions: InstanceTypeLifecycleAction[];
}

export interface DiskImageTestData extends DiskImageCreateParams {
  lifecycleActions: string[];
}

const extravarsPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../extravars.yaml',
);

const {
  instance_type: rawInstanceType,
  disk_image: rawDiskImage,
  lifecycle_actions: rawLifecycleActions,
  tenant_name: rawTenantName,
} = parse(readFileSync(extravarsPath, 'utf8')) as RawExtravars;

export const tenantName = rawTenantName;

export const instanceTypeTestData: InstanceTypeTestData = {
  name: rawInstanceType.name,
  description: rawInstanceType.description,
  vcpus: rawInstanceType.vcpus,
  memoryGib: rawInstanceType.memory_gib,
  gpu: rawInstanceType.gpu
    ? {
        count: rawInstanceType.gpu.count,
        resourceName: rawInstanceType.gpu.resource_name,
        pciDeviceSelector: rawInstanceType.gpu.pci_device_selector,
      }
    : undefined,
  lifecycleActions: rawLifecycleActions,
};

export const diskImageTestData: DiskImageTestData = {
  name: rawDiskImage.name,
  sourceType: rawDiskImage.source_type,
  sourceRef: rawDiskImage.source_ref,
  guestOsFamily: rawDiskImage.guest_os_family,
  architectures: rawDiskImage.architectures,
  scope: rawDiskImage.scope,
  lifecycleActions: rawLifecycleActions,
};
