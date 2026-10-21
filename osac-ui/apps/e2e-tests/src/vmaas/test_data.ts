import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

import type {
  DiskImageCreateParams,
  DiskImageLifecycleAction,
} from '../../../../libs/test-utils/src/disk_images.page';
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
  lifecycle_actions: InstanceTypeLifecycleAction[];
}

interface RawDiskImage {
  source_type: string;
  source_ref: string;
  guest_os_family: string;
  architectures: [string, ...string[]];
  scope: string;
  lifecycle_actions: DiskImageLifecycleAction[];
}

interface RawExtravars {
  instance_type: RawInstanceType;
  disk_image: RawDiskImage;
}

export interface InstanceTypeTestData {
  name: string;
  description: string;
  vcpus: number;
  memoryGib: number;
  gpu?: InstanceTypeGpu;
  lifecycleActions: InstanceTypeLifecycleAction[];
}

export interface DiskImageTestData extends Omit<DiskImageCreateParams, 'name'> {
  lifecycleActions: DiskImageLifecycleAction[];
}

const extravarsPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../extravars.yaml',
);

const { instance_type: rawInstanceType, disk_image: rawDiskImage } = parse(
  readFileSync(extravarsPath, 'utf8'),
) as RawExtravars;

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
  lifecycleActions: rawInstanceType.lifecycle_actions,
};

export const diskImageTestData: DiskImageTestData = {
  sourceType: rawDiskImage.source_type,
  sourceRef: rawDiskImage.source_ref,
  guestOsFamily: rawDiskImage.guest_os_family,
  architectures: rawDiskImage.architectures,
  scope: rawDiskImage.scope,
  lifecycleActions: rawDiskImage.lifecycle_actions,
};
